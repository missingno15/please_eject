package scan

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// procInfo is one row from ps, plus the working-directory details we can see
// for processes we own. Root processes (terminal login) leave Cwd and EnvPWD
// empty because a normal user cannot inspect them.
type procInfo struct {
	PID, PPID, UID int
	User, TTY      string
	Command        string
	EnvPWD         string // PWD from the process environment, when visible
	Cwd            string // kernel cwd, when visible
}

// mergeAnchors adds holders that lsof misses. See Process.Anchor.
func mergeAnchors(procs []Process, mount string, dissenterPIDs []int) []Process {
	table, err := readProcTable()
	if err != nil || len(table) == 0 {
		return procs
	}
	if pids := detailPIDs(table, dissenterPIDs); len(pids) > 0 {
		fillEnvPWD(table, pids)
		fillCwd(table, pids)
	}
	extra := selectAnchors(mount, table, procs, dissenterPIDs)
	if len(extra) == 0 {
		return procs
	}
	return append(procs, extra...)
}

// selectAnchors decides which processes to add on top of lsof's list.
//
// A terminal login keeps the cwd it was started with. Shells such as fish
// leave that path in the process environment after the user cd's away, while
// the kernel cwd (what lsof reports) moves. When those two disagree and only
// the environment path is on the volume, the hidden login is the dissenter.
//
// dissenterPIDs are included unconditionally when they are still alive: Disk
// Arbitration already said they are blocking the unmount, including the case
// where the shell updates PWD on cd and the heuristic above cannot see it.
func selectAnchors(mount string, table []procInfo, already []Process, dissenterPIDs []int) []Process {
	byPID := make(map[int]procInfo, len(table))
	children := map[int][]int{}
	for _, p := range table {
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	seen := map[int]bool{}
	for _, p := range already {
		seen[p.PID] = true
	}

	var out []Process
	for _, p := range table {
		if !isTerminalLogin(p.Command) || seen[p.PID] {
			continue
		}
		anchor, tab := staleAnchor(p.PID, children, byPID, mount)
		if anchor == "" {
			continue
		}
		out = append(out, makeSession(p, byPID, children, anchor, tab, false))
		seen[p.PID] = true
	}
	for _, id := range dissenterPIDs {
		if seen[id] {
			continue
		}
		p, ok := byPID[id]
		if !ok {
			continue
		}
		if isTerminalLogin(p.Command) {
			anchor, tab := staleAnchor(p.PID, children, byPID, mount)
			out = append(out, makeSession(p, byPID, children, anchor, tab, true))
		} else {
			out = append(out, makeDissenter(p))
		}
		seen[id] = true
	}
	return out
}

// staleAnchor reports the shell's original directory when it is still on the
// volume but the shell's live cwd is not. That original directory is the one
// the parent login process is still holding.
func staleAnchor(root int, children map[int][]int, byPID map[int]procInfo, mount string) (anchor, tab string) {
	for _, id := range descendants(root, children) {
		p := byPID[id]
		if p.Cwd == "" || p.EnvPWD == "" {
			continue
		}
		if !onMount(p.EnvPWD, mount) || onMount(p.Cwd, mount) {
			continue
		}
		if filepath.Clean(p.EnvPWD) == filepath.Clean(p.Cwd) {
			continue
		}
		return p.EnvPWD, p.Cwd
	}
	return "", ""
}

func makeSession(login procInfo, byPID map[int]procInfo, children map[int][]int, anchor, tab string, fromDissenter bool) Process {
	app := sessionApp(login.PID, byPID)
	user, uid := sessionUser(login, children, byPID)
	tty := login.TTY
	if tty == "??" {
		tty = ""
	}
	p := Process{
		PID:     login.PID,
		Command: app + " session",
		User:    user,
		UID:     uid,
		Anchor:  true,
		Reason:  sessionReason(app, tty, anchor, tab, fromDissenter),
	}
	if anchor != "" {
		p.Files = []OpenFile{{FD: "cwd", Type: "DIR", Path: anchor}}
	}
	return p
}

func makeDissenter(p procInfo) Process {
	token := firstToken(p.Command)
	proc := Process{
		PID:     p.PID,
		Command: filepath.Base(token),
		User:    p.User,
		UID:     p.UID,
	}
	if strings.Contains(token, "/") {
		proc.ExePath = token
	}
	if reason, ok := isSystem(proc); ok {
		proc.System = true
		proc.Reason = reason
		return proc
	}
	proc.Reason = "blocked eject (Disk Arbitration). Stop it to release the volume."
	return proc
}

func sessionReason(app, tty, anchor, tab string, fromDissenter bool) string {
	where := ""
	if tty != "" {
		where = " (" + tty + ")"
	}
	switch {
	case anchor != "" && tab != "":
		return fmt.Sprintf("%s tab%s is in %s, but the session is still anchored at %s. Stopping it closes the tab.",
			app, where, displayPath(tab), displayPath(anchor))
	case fromDissenter:
		return fmt.Sprintf("%s session%s blocked eject. Stopping it closes the tab.", app, where)
	default:
		return fmt.Sprintf("%s session%s is still holding this volume. Stopping it closes the tab.", app, where)
	}
}

func sessionApp(pid int, byPID map[int]procInfo) string {
	for id := pid; id != 0; {
		p, ok := byPID[id]
		if !ok {
			break
		}
		cmd := p.Command
		switch {
		case strings.Contains(cmd, "iTerm"):
			return "iTerm"
		case strings.Contains(cmd, "Terminal.app"):
			return "Terminal"
		case strings.Contains(cmd, "Ghostty"), strings.Contains(cmd, "ghostty"):
			return "Ghostty"
		case strings.Contains(cmd, "WezTerm"), strings.Contains(cmd, "wezterm"):
			return "WezTerm"
		case strings.Contains(cmd, "kitty"):
			return "kitty"
		case strings.Contains(cmd, "Alacritty"), strings.Contains(cmd, "alacritty"):
			return "Alacritty"
		}
		id = p.PPID
	}
	return "Terminal"
}

func sessionUser(login procInfo, children map[int][]int, byPID map[int]procInfo) (string, int) {
	for _, id := range descendants(login.PID, children) {
		p := byPID[id]
		if p.UID != 0 && p.User != "" && p.User != "root" {
			return p.User, p.UID
		}
	}
	if login.UID != 0 && login.User != "" && login.User != "root" {
		return login.User, login.UID
	}
	return login.User, login.UID
}

func isTerminalLogin(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return filepath.Base(fields[0]) == "login"
}

func onMount(path, mount string) bool {
	path = filepath.Clean(path)
	mount = filepath.Clean(mount)
	if path == "" || path == "." || mount == "" || mount == "." {
		return false
	}
	if path == mount {
		return true
	}
	rel, err := filepath.Rel(mount, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = filepath.Clean(home)
	path = filepath.Clean(path)
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

func descendants(root int, children map[int][]int) []int {
	var out []int
	stack := append([]int{}, children[root]...)
	seen := map[int]bool{root: true}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		stack = append(stack, children[id]...)
	}
	return out
}

// detailPIDs are the processes whose cwd and PWD we try to read: shells under
// a terminal login, plus any dissenter and its children.
func detailPIDs(table []procInfo, dissenters []int) []int {
	children := map[int][]int{}
	for _, p := range table {
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	seen := map[int]bool{}
	var pids []int
	add := func(id int) {
		if id <= 0 || seen[id] {
			return
		}
		seen[id] = true
		pids = append(pids, id)
	}
	addTree := func(root int, includeRoot bool) {
		if includeRoot {
			add(root)
		}
		for _, id := range descendants(root, children) {
			add(id)
		}
	}
	for _, p := range table {
		if isTerminalLogin(p.Command) {
			addTree(p.PID, false)
		}
	}
	for _, id := range dissenters {
		addTree(id, true)
	}
	return pids
}

func readProcTable() ([]procInfo, error) {
	out, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=,uid=,user=,tty=,command=").Output()
	if err != nil {
		return nil, err
	}
	var table []procInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		p, ok := parsePSLine(sc.Text())
		if ok {
			table = append(table, p)
		}
	}
	return table, nil
}

func parsePSLine(line string) (procInfo, bool) {
	// pid ppid uid user tty command
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return procInfo{}, false
	}
	pid, err1 := strconv.Atoi(fields[0])
	ppid, err2 := strconv.Atoi(fields[1])
	uid, err3 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return procInfo{}, false
	}
	// Command is the remainder of the raw line after the tty column, so paths
	// with spaces survive Fields' splitting of the fixed prefix.
	rest := strings.TrimSpace(line)
	for i := 0; i < 5; i++ {
		sp := strings.IndexAny(rest, " \t")
		if sp < 0 {
			return procInfo{}, false
		}
		rest = strings.TrimSpace(rest[sp:])
	}
	return procInfo{
		PID:     pid,
		PPID:    ppid,
		UID:     uid,
		User:    fields[3],
		TTY:     fields[4],
		Command: rest,
	}, true
}

func fillEnvPWD(table []procInfo, pids []int) {
	if len(pids) == 0 {
		return
	}
	// -E is the launch environment (it does not follow later cd). -ww keeps long
	// paths from being truncated. That launch PWD is the directory the parent
	// login process is still holding.
	out, err := exec.Command("ps", "-E", "-ww", "-p", joinPIDs(pids), "-o", "pid=,command=").Output()
	if err != nil {
		return
	}
	pwds := map[int]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		sp := strings.IndexAny(line, " \t")
		if sp < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:sp])
		if err != nil {
			continue
		}
		if pwd := lastEnvValue(line[sp:], "PWD"); pwd != "" {
			pwds[pid] = pwd
		}
	}
	for i := range table {
		if pwd, ok := pwds[table[i].PID]; ok {
			table[i].EnvPWD = pwd
		}
	}
}

func fillCwd(table []procInfo, pids []int) {
	if len(pids) == 0 {
		return
	}
	out, err := exec.Command("lsof", "-a", "-w", "-p", joinPIDs(pids), "-d", "cwd", "-F", "pn").Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return
	}
	cwds := map[int]string{}
	var pid int
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid != 0 {
				cwds[pid] = line[1:]
			}
		}
	}
	for i := range table {
		if cwd, ok := cwds[table[i].PID]; ok {
			table[i].Cwd = cwd
		}
	}
}

func joinPIDs(pids []int) string {
	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = strconv.Itoa(pid)
	}
	return strings.Join(parts, ",")
}

func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return command
	}
	return fields[0]
}

// lastEnvValue returns the last KEY=value in s. ps -eww appends the environment
// after the command line, and values may contain spaces, so the value runs up
// to the next KEY= token.
func lastEnvValue(s, key string) string {
	needle := key + "="
	var found string
	rest := s
	for {
		i := strings.Index(rest, needle)
		if i < 0 {
			break
		}
		if i == 0 || rest[i-1] == ' ' || rest[i-1] == '\t' {
			val := rest[i+len(needle):]
			if cut := nextEnvKey(val); cut >= 0 {
				found = strings.TrimSpace(val[:cut])
			} else {
				found = strings.TrimSpace(val)
			}
		}
		rest = rest[i+len(needle):]
	}
	return found
}

// nextEnvKey reports the index of the space before the next environment
// assignment in s, or -1 if s has no further KEY= token.
func nextEnvKey(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			continue
		}
		j := i + 1
		k := j
		if k >= len(s) || !isEnvKeyStart(s[k]) {
			continue
		}
		k++
		for k < len(s) && isEnvKeyCont(s[k]) {
			k++
		}
		if k > j && k < len(s) && s[k] == '=' {
			return i
		}
	}
	return -1
}

func isEnvKeyStart(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z')
}

func isEnvKeyCont(b byte) bool {
	return isEnvKeyStart(b) || (b >= '0' && b <= '9')
}
