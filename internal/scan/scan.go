// Package scan discovers which processes are holding a volume open using lsof,
// and enriches the results so they can be presented and acted upon safely.
package scan

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OpenFile is a single file/handle a process holds on the volume.
type OpenFile struct {
	FD   string // file descriptor: "cwd", "txt", "18", ...
	Type string // REG, DIR, ...
	Path string
}

// Process is one program holding the volume open, along with everything it has
// open on that volume.
type Process struct {
	PID     int
	Command string // lsof's short command name
	ExePath string // full executable path, resolved via ps (best effort)
	User    string
	UID     int
	Files   []OpenFile

	// System is true when this looks like an OS-owned/critical process that the
	// user should think twice before force-killing.
	System bool
	// Reason explains why System is set (empty when not a system process).
	Reason string
}

// Label returns the friendliest name available for the process.
func (p Process) Label() string {
	if p.ExePath != "" {
		return filepath.Base(p.ExePath)
	}
	return p.Command
}

// systemCommands are macOS daemons/agents that commonly hold external volumes
// (Spotlight indexing, Quick Look, Time Machine, antivirus, Finder helpers).
// Killing these is usually unnecessary and sometimes disruptive, so we warn.
var systemCommands = map[string]string{
	"mds":                 "Spotlight indexing",
	"mds_stores":          "Spotlight indexing",
	"mdworker":            "Spotlight indexing worker",
	"mdworker_shared":     "Spotlight indexing worker",
	"mdbulkimport":        "Spotlight import",
	"mdsync":              "Spotlight sync",
	"quicklookd":          "Quick Look preview daemon",
	"QuickLookUIService":  "Quick Look preview",
	"com.apple.quicklook": "Quick Look preview",
	"fseventsd":           "File system events daemon",
	"diskarbitrationd":    "Disk Arbitration daemon",
	"backupd":             "Time Machine backup",
	"backupd-helper":      "Time Machine backup helper",
	"Finder":              "Finder",
	"Dock":                "Dock",
	"Spotlight":           "Spotlight",
	"corespotlightd":      "CoreSpotlight",
	"bird":                "iCloud/Files sync",
	"cloudd":              "iCloud sync",
	"antivirus":           "Antivirus",
}

// Scan runs lsof against the given mount point and returns the processes
// holding it, sorted with user processes first, then by PID.
func Scan(mountPoint string) ([]Process, error) {
	// Passing a mount point to lsof lists every open file on that filesystem,
	// regardless of directory depth. -F gives machine-readable output.
	cmd := exec.Command("lsof", "-w", "-F", "pcuLtn", "--", mountPoint)
	out, err := cmd.Output()
	if err != nil {
		// lsof exits non-zero (1) when it simply finds nothing. Treat empty
		// output as "no processes" rather than an error.
		if len(bytes.TrimSpace(out)) == 0 {
			return nil, nil
		}
		// Otherwise still try to parse what we got.
	}

	procs := parse(out)
	enrich(procs)

	sort.Slice(procs, func(a, b int) bool {
		if procs[a].System != procs[b].System {
			return !procs[a].System // non-system first
		}
		return procs[a].PID < procs[b].PID
	})
	return procs, nil
}

func parse(out []byte) []Process {
	var procs []Process
	var cur *Process
	var curFile *OpenFile

	flushFile := func() {
		if cur != nil && curFile != nil {
			cur.Files = append(cur.Files, *curFile)
			curFile = nil
		}
	}
	flushProc := func() {
		flushFile()
		if cur != nil {
			procs = append(procs, *cur)
			cur = nil
		}
	}

	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			flushProc()
			pid, _ := strconv.Atoi(val)
			cur = &Process{PID: pid}
		case 'c':
			if cur != nil {
				cur.Command = val
			}
		case 'u':
			if cur != nil {
				cur.UID, _ = strconv.Atoi(val)
			}
		case 'L':
			if cur != nil {
				cur.User = val
			}
		case 'f':
			flushFile()
			curFile = &OpenFile{FD: val}
		case 't':
			if curFile != nil {
				curFile.Type = val
			}
		case 'n':
			if curFile != nil {
				curFile.Path = val
			}
		}
	}
	flushProc()
	return procs
}

// enrich resolves full executable paths via ps and flags system processes.
func enrich(procs []Process) {
	if len(procs) == 0 {
		return
	}
	paths := execPaths(procs)
	for i := range procs {
		p := &procs[i]
		if full, ok := paths[p.PID]; ok {
			p.ExePath = full
		}
		if reason, ok := isSystem(*p); ok {
			p.System = true
			p.Reason = reason
		}
	}
}

// execPaths batches a single ps call to map PID -> full command path.
func execPaths(procs []Process) map[int]string {
	ids := make([]string, 0, len(procs))
	seen := map[int]bool{}
	for _, p := range procs {
		if !seen[p.PID] {
			seen[p.PID] = true
			ids = append(ids, strconv.Itoa(p.PID))
		}
	}
	out, err := exec.Command("ps", "-p", strings.Join(ids, ","), "-o", "pid=,comm=").Output()
	if err != nil {
		return nil
	}
	res := map[int]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.SplitN(strings.TrimSpace(sc.Text()), " ", 2)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		res[pid] = strings.TrimSpace(fields[1])
	}
	return res
}

func isSystem(p Process) (string, bool) {
	// root-owned processes are almost always system daemons.
	candidates := []string{p.Command, filepath.Base(p.ExePath)}
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if reason, ok := systemCommands[name]; ok {
			return reason, true
		}
	}
	if p.UID == 0 {
		return "root-owned system process", true
	}
	return "", false
}
