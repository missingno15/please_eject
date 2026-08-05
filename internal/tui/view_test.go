package tui

import (
	"strings"
	"testing"
)

func TestViewRendersAllStates(t *testing.T) {
	states := []state{stateScanning, stateList, stateConfirmKillAll, stateKilling, stateEjecting, stateClear, stateEjected, stateFatal}
	for _, st := range states {
		m := newTestModel()
		next, _ := m.Update(scanDoneMsg{procs: sampleProcs()})
		m = next.(model)
		m.state = st
		if st == stateConfirmKillAll {
			m.pending = m.killableAll()
		}
		if st == stateFatal {
			m.fatal = errStub{}
		}
		out := m.View()
		if !strings.Contains(out, "please_eject") {
			t.Fatalf("state %v: header missing", st)
		}
	}
}

type errStub struct{}

func (errStub) Error() string { return "boom" }
