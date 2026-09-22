package scan

import "testing"

func TestOnMount(t *testing.T) {
	mount := "/Volumes/My Passport for Mac"
	cases := []struct {
		path string
		want bool
	}{
		{mount, true},
		{mount + "/vr-player", true},
		{mount + "/", true},
		{"/Volumes/My Passport", false},
		{"/Volumes/My Passport for Mac Backup", false},
		{"/Users/kennethuy/Downloads", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := onMount(tc.path, mount); got != tc.want {
			t.Errorf("onMount(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestLastEnvValueKeepsSpaces(t *testing.T) {
	line := "/opt/homebrew/bin/fish --login PWD=/Volumes/My Passport for Mac/vr-player SHELL=/opt/homebrew/bin/fish USER=kennethuy"
	got := lastEnvValue(line, "PWD")
	want := "/Volumes/My Passport for Mac/vr-player"
	if got != want {
		t.Fatalf("PWD = %q, want %q", got, want)
	}
}

func TestParsePSLine(t *testing.T) {
	line := " 99216  1550     0 root             ttys013  /usr/bin/login -fpl kennethuy /Applications/iTerm.app/Contents/MacOS/ShellLauncher --launch_shell"
	p, ok := parsePSLine(line)
	if !ok {
		t.Fatal("expected to parse")
	}
	if p.PID != 99216 || p.PPID != 1550 || p.UID != 0 || p.User != "root" || p.TTY != "ttys013" {
		t.Fatalf("identity parsed wrong: %+v", p)
	}
	if p.Command != "/usr/bin/login -fpl kennethuy /Applications/iTerm.app/Contents/MacOS/ShellLauncher --launch_shell" {
		t.Fatalf("command = %q", p.Command)
	}
}

func TestSelectAnchorsFindsStaleITermLogin(t *testing.T) {
	mount := "/Volumes/My Passport for Mac"
	table := []procInfo{
		{PID: 1550, PPID: 1, UID: 501, User: "kennethuy", TTY: "??", Command: "/Users/kennethuy/Library/Application Support/iTerm2/iTermServer-3.7.1"},
		{PID: 436, PPID: 1, UID: 501, User: "kennethuy", TTY: "??", Command: "/System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow console"},
		{PID: 420, PPID: 1, UID: 0, User: "root", TTY: "??", Command: "/System/Library/CoreServices/logind"},
		// This tab cd'd to ~/Downloads; login is still anchored on the volume.
		{PID: 99216, PPID: 1550, UID: 0, User: "root", TTY: "ttys013", Command: "/usr/bin/login -fpl kennethuy /Applications/iTerm.app/Contents/MacOS/ShellLauncher --launch_shell"},
		{PID: 99217, PPID: 99216, UID: 501, User: "kennethuy", TTY: "ttys013", Command: "-fish", EnvPWD: mount + "/vr-player", Cwd: "/Users/kennethuy/Downloads"},
		// This tab is actually on the volume, so lsof already lists the shell.
		{PID: 1553, PPID: 1550, UID: 0, User: "root", TTY: "ttys000", Command: "/usr/bin/login -fpl kennethuy /Applications/iTerm.app/Contents/MacOS/ShellLauncher"},
		{PID: 1560, PPID: 1553, UID: 501, User: "kennethuy", TTY: "ttys000", Command: "fish", EnvPWD: mount + "/photos", Cwd: mount + "/photos"},
		// Started at home and still at home. login's cwd is unknown; don't guess.
		{PID: 53082, PPID: 1550, UID: 0, User: "root", TTY: "ttys015", Command: "/usr/bin/login -fpl kennethuy"},
		{PID: 53083, PPID: 53082, UID: 501, User: "kennethuy", TTY: "ttys015", Command: "-fish", EnvPWD: "/Users/kennethuy/Projects/please_eject", Cwd: "/Users/kennethuy/Projects/please_eject"},
	}

	got := selectAnchors(mount, table, nil, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(got), got)
	}
	p := got[0]
	if p.PID != 99216 {
		t.Fatalf("pid = %d, want 99216", p.PID)
	}
	if !p.Anchor || p.System {
		t.Fatalf("want a stoppable anchor, got anchor=%v system=%v", p.Anchor, p.System)
	}
	if p.Command != "iTerm session" {
		t.Fatalf("command = %q", p.Command)
	}
	if p.User != "kennethuy" || p.UID != 501 {
		t.Fatalf("user = %s uid = %d, want kennethuy/501", p.User, p.UID)
	}
	if len(p.Files) != 1 || p.Files[0].Path != mount+"/vr-player" {
		t.Fatalf("files = %+v", p.Files)
	}
	if p.Reason == "" {
		t.Fatal("expected a reason naming the tab")
	}
}

func TestSelectAnchorsIncludesNamedDissenter(t *testing.T) {
	mount := "/Volumes/My Passport for Mac"
	table := []procInfo{
		{PID: 1550, PPID: 1, UID: 501, User: "kennethuy", Command: "iTermServer"},
		// Shell updates PWD, so the stale-cwd heuristic cannot see the hold.
		{PID: 42, PPID: 1550, UID: 0, User: "root", TTY: "ttys001", Command: "/usr/bin/login -fpl kennethuy"},
		{PID: 43, PPID: 42, UID: 501, User: "kennethuy", TTY: "ttys001", Command: "-zsh", EnvPWD: "/Users/kennethuy", Cwd: "/Users/kennethuy"},
	}
	if got := selectAnchors(mount, table, nil, nil); len(got) != 0 {
		t.Fatalf("heuristic should not guess, got %+v", got)
	}
	got := selectAnchors(mount, table, nil, []int{42})
	if len(got) != 1 || got[0].PID != 42 || !got[0].Anchor || got[0].System {
		t.Fatalf("dissenter login = %+v", got)
	}
}

func TestSelectAnchorsSkipsExistingAndRootDaemons(t *testing.T) {
	mount := "/Volumes/Disk"
	table := []procInfo{
		{PID: 9, PPID: 1, UID: 0, User: "root", Command: "/usr/libexec/mds"},
	}
	got := selectAnchors(mount, table, nil, []int{9})
	if len(got) != 1 || !got[0].System {
		t.Fatalf("root dissenter should stay a system process, got %+v", got)
	}
}
