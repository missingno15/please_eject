// Package tui implements the interactive terminal UI for please_eject.
package tui

import (
	"fmt"
	"time"

	"please_eject/internal/kill"
	"please_eject/internal/scan"
	"please_eject/internal/volume"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type state int

const (
	stateScanning state = iota
	stateList
	stateConfirmKillAll
	stateKilling
	stateEjecting
	stateClear // volume free; offer to eject
	stateEjected
	stateFatal
)

const gracePeriod = 4 * time.Second

// ---- Messages ----

type scanDoneMsg struct {
	procs     []scan.Process
	spotlight string // "on" / "off" / "not indexed" / ""
	err       error
}

type killDoneMsg struct {
	results []kill.Result
}

type ejectDoneMsg struct {
	err error
}

type spotlightDoneMsg struct {
	err error
}

// ---- Model ----

type model struct {
	vol volume.Info

	state   state
	spinner spinner.Model

	procs   []scan.Process
	cursor  int
	marked  map[int]bool   // PID set marked for batch kill
	pending []scan.Process // processes queued for the current kill action

	results []kill.Result // outcome of last kill
	status  string        // transient status line
	fatal   error         // unrecoverable error
	ejErr   string        // last eject failure message

	spotlight string // volume's Spotlight indexing state: on/off/not indexed

	// dissenters are PIDs Disk Arbitration named while refusing an eject.
	// They are fed back into the next scan so a root terminal login that lsof
	// cannot see still shows up in the list.
	dissenters []int

	width, height int
}

// New builds the initial model for the given resolved volume.
func New(vol volume.Info) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle
	return model{
		vol:     vol,
		state:   stateScanning,
		spinner: sp,
		marked:  map[int]bool{},
		status:  "Scanning for programs using the volume…",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.rescan())
}

// ---- Commands ----

func (m model) rescan() tea.Cmd {
	vol := m.vol
	pids := append([]int(nil), m.dissenters...)
	return func() tea.Msg {
		procs, err := scan.Scan(vol.MountPoint, pids...)
		return scanDoneMsg{procs: procs, spotlight: vol.SpotlightStatus(), err: err}
	}
}

func killCmd(procs []scan.Process) tea.Cmd {
	return func() tea.Msg {
		results := make([]kill.Result, 0, len(procs))
		for _, p := range procs {
			results = append(results, kill.Graceful(p.PID, gracePeriod))
		}
		return killDoneMsg{results: results}
	}
}

func ejectCmd(vol volume.Info) tea.Cmd {
	return func() tea.Msg {
		return ejectDoneMsg{err: vol.Eject()}
	}
}

// spotlightOffCmd releases the terminal so the user can enter their sudo
// password, runs `sudo mdutil -i off <mount>`, then resumes the TUI.
func spotlightOffCmd(vol volume.Info) tea.Cmd {
	return tea.ExecProcess(vol.SpotlightOffCmd(), func(err error) tea.Msg {
		return spotlightDoneMsg{err: err}
	})
}

// ---- Update ----

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scanDoneMsg:
		return m.onScanDone(msg)

	case killDoneMsg:
		m.results = msg.results
		// After killing, always rescan to confirm the real state.
		m.state = stateScanning
		m.status = "Rechecking the volume…"
		return m, tea.Batch(m.spinner.Tick, m.rescan())

	case ejectDoneMsg:
		return m.onEjectDone(msg)

	case spotlightDoneMsg:
		if msg.err != nil {
			m.status = "Spotlight nudge failed: " + msg.err.Error()
		} else {
			m.status = "Told Spotlight to stop indexing this volume. Rechecking…"
		}
		// Give mds a moment to let go, then rescan.
		m.state = stateScanning
		return m, tea.Batch(m.spinner.Tick, m.rescan())

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m model) onScanDone(msg scanDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fatal = msg.err
		m.state = stateFatal
		return m, nil
	}
	m.procs = msg.procs
	m.spotlight = msg.spotlight
	// Drop marks/cursor for processes that no longer exist.
	m.reconcile()
	m.dissenters = livePIDs(m.dissenters, m.procs)
	if len(m.procs) == 0 {
		m.state = stateClear
		m.status = ""
		return m, nil
	}
	m.state = stateList
	if hasAnchor(m.procs) {
		m.status = "A terminal session is still holding this volume. Stopping it closes that tab."
	}
	return m, nil
}

func (m model) onEjectDone(msg ejectDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.ejErr = msg.err.Error()
		m.status = "Eject failed — rescanning to see what's holding it…"
		if pid, ok := volume.DissenterPID(m.ejErr); ok {
			m.dissenters = addPID(m.dissenters, pid)
			m.ejErr = ""
			m.status = "Eject was refused by a process lsof didn't list. Checking it…"
		}
		m.state = stateScanning
		return m, tea.Batch(m.spinner.Tick, m.rescan())
	}
	m.state = stateEjected
	return m, tea.Quit
}

func hasAnchor(procs []scan.Process) bool {
	for _, p := range procs {
		if p.Anchor {
			return true
		}
	}
	return false
}

func addPID(ids []int, pid int) []int {
	for _, id := range ids {
		if id == pid {
			return ids
		}
	}
	return append(ids, pid)
}

func livePIDs(ids []int, procs []scan.Process) []int {
	live := map[int]bool{}
	for _, p := range procs {
		live[p.PID] = true
	}
	var kept []int
	for _, id := range ids {
		if live[id] {
			kept = append(kept, id)
		}
	}
	return kept
}

func (m *model) reconcile() {
	if m.cursor >= len(m.procs) {
		m.cursor = max(0, len(m.procs)-1)
	}
	live := map[int]bool{}
	for _, p := range m.procs {
		live[p.PID] = true
	}
	for pid := range m.marked {
		if !live[pid] {
			delete(m.marked, pid)
		}
	}
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateList:
		return m.onListKey(msg)
	case stateConfirmKillAll:
		return m.onConfirmKey(msg)
	case stateClear:
		switch msg.String() {
		case "e", "enter":
			m.state = stateEjecting
			m.status = "Ejecting…"
			return m, tea.Batch(m.spinner.Tick, ejectCmd(m.vol))
		case "r":
			m.state = stateScanning
			m.status = "Rescanning…"
			return m, tea.Batch(m.spinner.Tick, m.rescan())
		case "s":
			return m.nudgeSpotlight()
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	case stateFatal, stateEjected:
		return m, tea.Quit
	default: // scanning / killing / ejecting
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) onListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.procs)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(m.procs) - 1
	case " ":
		p := m.procs[m.cursor]
		if m.marked[p.PID] {
			delete(m.marked, p.PID)
		} else {
			m.marked[p.PID] = true
		}
	case "x", "enter":
		// Stop the highlighted process (or all marked ones if any are marked).
		targets := m.markedProcs()
		if len(targets) == 0 {
			targets = []scan.Process{m.procs[m.cursor]}
		}
		m.pending = targets
		return m.beginKill()
	case "a":
		// Kill ALL — but only user processes; system ones must be explicit.
		m.pending = m.killableAll()
		if len(m.pending) == 0 {
			m.status = "Only system processes remain — stop those individually with x."
			return m, nil
		}
		m.state = stateConfirmKillAll
		return m, nil
	case "r":
		m.state = stateScanning
		m.status = "Rescanning…"
		return m, tea.Batch(m.spinner.Tick, m.rescan())
	case "s":
		// Nudge Spotlight to stop indexing (needs sudo; releases mds/mdworker).
		return m.nudgeSpotlight()
	}
	return m, nil
}

func (m model) onConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.beginKill()
	case "n", "N", "esc", "q":
		m.state = stateList
		m.pending = nil
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) beginKill() (tea.Model, tea.Cmd) {
	m.state = stateKilling
	m.status = fmt.Sprintf("Stopping %d program(s)…", len(m.pending))
	targets := m.pending
	m.pending = nil
	return m, tea.Batch(m.spinner.Tick, killCmd(targets))
}

// nudgeSpotlight turns off Spotlight indexing for the volume, unless indexing
// isn't active (then it's a no-op we skip rather than prompt for sudo).
func (m model) nudgeSpotlight() (tea.Model, tea.Cmd) {
	if m.spotlight == "off" {
		m.status = "Spotlight indexing is already off for this volume."
		return m, nil
	}
	if m.spotlight == "not indexed" {
		m.status = "Spotlight doesn't index this volume — nothing to disable."
		return m, nil
	}
	m.status = "Disabling Spotlight indexing (enter your password below)…"
	return m, spotlightOffCmd(m.vol)
}

func (m model) markedProcs() []scan.Process {
	var out []scan.Process
	for _, p := range m.procs {
		if m.marked[p.PID] {
			out = append(out, p)
		}
	}
	return out
}

func (m model) killableAll() []scan.Process {
	var out []scan.Process
	for _, p := range m.procs {
		if !p.System {
			out = append(out, p)
		}
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
