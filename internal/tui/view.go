package tui

import (
	"fmt"
	"strings"

	"please_eject/internal/kill"
	"please_eject/internal/scan"

	"github.com/charmbracelet/lipgloss"
)

// ---- Styles ----

var (
	colorAccent = lipgloss.Color("39")  // blue
	colorGood   = lipgloss.Color("42")  // green
	colorWarn   = lipgloss.Color("214") // orange
	colorBad    = lipgloss.Color("203") // red
	colorMuted  = lipgloss.Color("245") // gray

	spinnerStyle = lipgloss.NewStyle().Foreground(colorAccent)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(colorAccent).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().Foreground(colorMuted)

	cursorStyle   = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	normalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	markStyle     = lipgloss.NewStyle().Foreground(colorBad).Bold(true)
	sysStyle      = lipgloss.NewStyle().Foreground(colorWarn)
	pathStyle     = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)

	goodStyle = lipgloss.NewStyle().Foreground(colorGood).Bold(true)
	badStyle  = lipgloss.NewStyle().Foreground(colorBad).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)

	helpStyle = lipgloss.NewStyle().Foreground(colorMuted)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorWarn).
			Padding(1, 2)
)

// ---- View ----

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")

	switch m.state {
	case stateScanning, stateKilling, stateEjecting:
		b.WriteString(fmt.Sprintf("%s %s\n", m.spinner.View(), m.status))
	case stateList:
		b.WriteString(m.viewList())
	case stateConfirmKillAll:
		b.WriteString(m.viewList())
		b.WriteString("\n")
		b.WriteString(m.viewConfirm())
	case stateClear:
		b.WriteString(m.viewClear())
	case stateEjected:
		b.WriteString(goodStyle.Render("Ejected. Safe to unplug ✔"))
		b.WriteString("\n")
	case stateFatal:
		b.WriteString(badStyle.Render("Error: ") + m.fatal.Error() + "\n")
	}

	if lastResults := m.viewResults(); lastResults != "" {
		b.WriteString("\n")
		b.WriteString(lastResults)
	}
	return b.String()
}

func (m model) header() string {
	title := titleStyle.Render("please_eject")
	sub := subtitleStyle.Render(fmt.Sprintf("  %s  •  %s", m.vol.Name, m.vol.MountPoint))
	if m.spotlight != "" {
		sub += subtitleStyle.Render(fmt.Sprintf("  •  Spotlight: %s", m.spotlight))
	}
	return title + sub
}

func (m model) viewList() string {
	var b strings.Builder

	n := len(m.procs)
	nSys := 0
	for _, p := range m.procs {
		if p.System {
			nSys++
		}
	}
	summary := fmt.Sprintf("%d program(s) are using this volume", n)
	if nSys > 0 {
		summary += fmt.Sprintf("  (%d system)", nSys)
	}
	b.WriteString(warnStyle.Render(summary))
	if m.ejErr != "" {
		b.WriteString("\n" + badStyle.Render("Eject blocked: ") + subtitleStyle.Render(m.ejErr))
	}
	b.WriteString("\n\n")

	for i, p := range m.procs {
		b.WriteString(m.viewRow(i, p))
		b.WriteString("\n")
	}

	if m.status != "" {
		b.WriteString("\n" + subtitleStyle.Render(m.status) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(
		"↑/↓ move • space mark • x stop (marked/current) • a stop all user progs • s stop Spotlight • r rescan • q quit"))
	return b.String()
}

func (m model) viewRow(i int, p scan.Process) string {
	cursor := "  "
	if i == m.cursor {
		cursor = cursorStyle.Render("▸ ")
	}

	mark := "[ ]"
	if m.marked[p.PID] {
		mark = markStyle.Render("[x]")
	}

	name := p.Label()
	nameStyled := normalStyle.Render(name)
	if i == m.cursor {
		nameStyled = selectedStyle.Render(name)
	}

	line := fmt.Sprintf("%s%s  %-6d  %-24s %s",
		cursor, mark, p.PID, truncate(nameStyled, name, 24),
		subtitleStyle.Render(fmt.Sprintf("%s • %d file(s)", p.User, len(p.Files))))

	if p.System {
		line += "  " + sysStyle.Render("⚠ "+p.Reason)
	} else if p.Anchor && p.Reason != "" {
		line += "\n      " + warnStyle.Render(p.Reason)
	}

	// Show a sample open path under the highlighted row for context.
	if i == m.cursor && len(p.Files) > 0 {
		sample := p.Files[0].Path
		for _, f := range p.Files {
			if f.Type == "REG" { // prefer a real file over cwd/dir
				sample = f.Path
				break
			}
		}
		line += "\n      " + pathStyle.Render("↳ "+sample)
	}
	return line
}

func (m model) viewConfirm() string {
	n := len(m.pending)
	skipped := len(m.procs) - n
	msg := badStyle.Render(fmt.Sprintf("Stop all %d user program(s)?", n))
	msg += "\n"
	msg += subtitleStyle.Render("They'll get a chance to close gracefully (SIGTERM), then be force-killed if stuck.")
	if skipped > 0 {
		msg += "\n" + sysStyle.Render(fmt.Sprintf("%d system process(es) will be left alone — stop those individually if needed.", skipped))
	}
	msg += "\n\n" + goodStyle.Render("[y]") + " yes    " + normalStyle.Render("[n]") + " cancel"
	return boxStyle.Render(msg)
}

func (m model) viewClear() string {
	var b strings.Builder
	b.WriteString(goodStyle.Render("✔ No programs are using this volume anymore."))
	b.WriteString("\n\n")
	if m.ejErr != "" {
		b.WriteString(subtitleStyle.Render("(Previous eject error cleared.)\n\n"))
	}
	b.WriteString(normalStyle.Render("It should be safe to eject now."))
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("e/enter eject now • s stop Spotlight • r rescan • q quit"))
	return b.String()
}

func (m model) viewResults() string {
	if len(m.results) == 0 {
		return ""
	}
	var lines []string
	for _, r := range m.results {
		lines = append(lines, formatResult(r))
	}
	title := subtitleStyle.Render("Last actions:")
	return title + "\n" + strings.Join(lines, "\n")
}

func formatResult(r kill.Result) string {
	switch {
	case r.Err != nil:
		return badStyle.Render(fmt.Sprintf("  ✘ PID %d: %s", r.PID, r.Err.Error()))
	case r.AlreadyGone:
		return subtitleStyle.Render(fmt.Sprintf("  · PID %d: already gone", r.PID))
	case r.Escaped:
		return warnStyle.Render(fmt.Sprintf("  ✔ PID %d: force-killed (SIGKILL)", r.PID))
	case r.Signal == "TERM":
		return goodStyle.Render(fmt.Sprintf("  ✔ PID %d: stopped gracefully", r.PID))
	default:
		return goodStyle.Render(fmt.Sprintf("  ✔ PID %d: stopped", r.PID))
	}
}

// truncate renders a styled string but keeps column alignment based on the
// unstyled text width.
func truncate(styled, raw string, width int) string {
	if len(raw) <= width {
		return styled + strings.Repeat(" ", width-len(raw))
	}
	// Fall back to trimming the raw text (drops styling on overflow).
	return raw[:width-1] + "…"
}
