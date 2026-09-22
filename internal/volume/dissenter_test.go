package volume

import "testing"

func TestDissenterPID(t *testing.T) {
	msg := "Unmount of disk4 failed: at least one volume could not be unmounted\n" +
		"Unmount was dissented by PID 99216 (/usr/bin/login)\n" +
		"Dissenter parent PPID 1550 (/Users/kennethuy/Library/Application Support/iTerm2/iTermServer-3.7.1)\n"
	pid, ok := DissenterPID(msg)
	if !ok || pid != 99216 {
		t.Fatalf("got pid=%d ok=%v", pid, ok)
	}
	if _, ok := DissenterPID("Volume is busy"); ok {
		t.Fatal("expected no dissenter")
	}
}
