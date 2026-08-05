package tui

import (
	"testing"

	"please_eject/internal/scan"
	"please_eject/internal/volume"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel() model {
	return New(volume.Info{Name: "Test", MountPoint: "/Volumes/Test"})
}

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func sampleProcs() []scan.Process {
	return []scan.Process{
		{PID: 100, Command: "mediaplayer", User: "me", Files: []scan.OpenFile{{FD: "cwd", Type: "DIR", Path: "/Volumes/Test/x"}}},
		{PID: 200, Command: "editor", User: "me", Files: []scan.OpenFile{{FD: "3", Type: "REG", Path: "/Volumes/Test/y"}}},
		{PID: 902, Command: "QuickLookUIService", User: "me", System: true, Reason: "Quick Look preview"},
	}
}

func TestScanToList(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)
	if m.state != stateList {
		t.Fatalf("expected stateList, got %v", m.state)
	}
	if len(m.procs) != 3 {
		t.Fatalf("expected 3 procs, got %d", len(m.procs))
	}
}

func TestScanEmptyGoesClear(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: nil})
	m = next.(model)
	if m.state != stateClear {
		t.Fatalf("expected stateClear, got %v", m.state)
	}
}

func TestNavigationAndMark(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)

	next, _ = m.Update(key("down"))
	m = next.(model)
	if m.cursor != 1 {
		t.Fatalf("cursor should be 1, got %d", m.cursor)
	}

	next, _ = m.Update(key(" "))
	m = next.(model)
	if !m.marked[200] {
		t.Fatalf("PID 200 should be marked")
	}

	// Cursor should not go below 0.
	next, _ = m.Update(key("up"))
	m = next.(model)
	next, _ = m.Update(key("up"))
	m = next.(model)
	if m.cursor != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", m.cursor)
	}
}

func TestKillAllExcludesSystem(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)

	next, _ = m.Update(key("a"))
	m = next.(model)
	if m.state != stateConfirmKillAll {
		t.Fatalf("expected confirm state, got %v", m.state)
	}
	if len(m.pending) != 2 {
		t.Fatalf("expected 2 killable (system excluded), got %d", len(m.pending))
	}
	for _, p := range m.pending {
		if p.System {
			t.Fatalf("system process %d should not be queued for kill-all", p.PID)
		}
	}

	// Confirm kicks off the kill.
	next, _ = m.Update(key("y"))
	m = next.(model)
	if m.state != stateKilling {
		t.Fatalf("expected killing state after confirm, got %v", m.state)
	}
}

func TestCancelKillAll(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)
	next, _ = m.Update(key("a"))
	m = next.(model)
	next, _ = m.Update(key("n"))
	m = next.(model)
	if m.state != stateList {
		t.Fatalf("expected back to list after cancel, got %v", m.state)
	}
	if m.pending != nil {
		t.Fatalf("pending should be cleared after cancel")
	}
}

func TestManualKillCurrentWhenNoneMarked(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)
	// cursor at 0 (PID 100), press x -> should begin killing just that one.
	next, _ = m.Update(key("x"))
	m = next.(model)
	if m.state != stateKilling {
		t.Fatalf("expected killing state, got %v", m.state)
	}
}

func TestKillDoneTriggersRescan(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)
	next, cmd := m.Update(killDoneMsg{})
	m = next.(model)
	if m.state != stateScanning {
		t.Fatalf("expected rescan (scanning) after kill, got %v", m.state)
	}
	if cmd == nil {
		t.Fatalf("expected a rescan command")
	}
}

func TestReconcileDropsStaleMarks(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
	m = next.(model)
	m.marked[200] = true
	// New scan where 200 is gone.
	remaining := []scan.Process{sampleProcs()[0]}
	next, _ = m.Update(scanDoneMsg{procs: remaining})
	m = next.(model)
	if m.marked[200] {
		t.Fatalf("stale mark for PID 200 should be dropped")
	}
}

func TestSpotlightNudgeSkippedWhenNotIndexed(t *testing.T) {
	for _, st := range []string{"not indexed", "off"} {
		m := newTestModel()
		next, _ := m.Update(scanDoneMsg{procs: sampleProcs(), spotlight: st})
		m = next.(model)
		next, cmd := m.Update(key("s"))
		m = next.(model)
		if cmd != nil {
			t.Fatalf("spotlight=%q: expected no command (short-circuit), got one", st)
		}
		if m.status == "" {
			t.Fatalf("spotlight=%q: expected an explanatory status", st)
		}
	}
}

func TestSpotlightNudgeRunsWhenIndexingOn(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs(), spotlight: "on"})
	m = next.(model)
	_, cmd := m.Update(key("s"))
	if cmd == nil {
		t.Fatalf("expected a spotlight-off command when indexing is on")
	}
}

func TestSpotlightDoneTriggersRescan(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(scanDoneMsg{procs: sampleProcs(), spotlight: "on"})
	m = next.(model)
	next, cmd := m.Update(spotlightDoneMsg{})
	m = next.(model)
	if m.state != stateScanning {
		t.Fatalf("expected rescan after spotlight nudge, got %v", m.state)
	}
	if cmd == nil {
		t.Fatalf("expected a rescan command after spotlight nudge")
	}
}
