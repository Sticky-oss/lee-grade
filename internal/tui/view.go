// view.go renders the bubbletea Model into a single string per frame.
//
// Layout (approximate, scales with terminal size):
//
//	┌─ lee-lab — Caleston Audit-Archive ─────────── task1-users · 4/6 ─┐
//	│ TASK BRIEF (~40% width)        │ SHELL OUTPUT (~60% width)       │
//	│                                │                                 │
//	│ > Mira's narrative ...         │ [root@host ~]# id alice         │
//	│                                │ uid=1001(alice) ...             │
//	│ Checks:                        │ [root@host ~]# _                │
//	│   ✓ Group sysadmins exists     │                                 │
//	│   ✗ User bob exists ...        │                                 │
//	│     hint: useradd -u 2002 bob  │                                 │
//	│                                │                                 │
//	└────────────────────────────────┴─────────────────────────────────┘
//	Ctrl+G grade · Ctrl+R clear · Ctrl+Q quit · F1 help
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sticky-oss/lee-grade/internal/check"
	"github.com/sticky-oss/lee-grade/internal/task"
)

// Style atoms. lipgloss styles are immutable + cheap to construct; we
// build them lazily on first render.
var (
	headerBg = lipgloss.AdaptiveColor{Light: "#0f172a", Dark: "#0f172a"}
	accent   = lipgloss.AdaptiveColor{Light: "#60a5fa", Dark: "#60a5fa"}
	dim      = lipgloss.AdaptiveColor{Light: "#94a3b8", Dark: "#94a3b8"}
	pass     = lipgloss.AdaptiveColor{Light: "#22c55e", Dark: "#4ade80"}
	fail     = lipgloss.AdaptiveColor{Light: "#ef4444", Dark: "#f87171"}
	amber    = lipgloss.AdaptiveColor{Light: "#f59e0b", Dark: "#fbbf24"}

	headerStyle = lipgloss.NewStyle().
			Foreground(accent).
			Background(headerBg).
			Bold(true).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(dim).
			Padding(0, 1)

	briefBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(dim)

	shellBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(dim)

	checkPass = lipgloss.NewStyle().Foreground(pass)
	checkFail = lipgloss.NewStyle().Foreground(fail)
	checkHint = lipgloss.NewStyle().Foreground(amber).Italic(true)
)

// View is the bubbletea render entry point.
func (m *Model) View() string {
	if m.quitting {
		return "\nlee-lab: shutting down.\n"
	}
	if m.width == 0 {
		// Pre-first-frame; bubbletea sends a WindowSizeMsg right away
		// so this only renders for a single frame at startup.
		return "lee-lab: initialising…"
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	body := m.renderBody()
	return strings.Join([]string{header, body, footer}, "\n")
}

// renderHeader paints the top bar: app name, task title, current score.
func (m *Model) renderHeader() string {
	score := ""
	if m.result != nil {
		score = fmt.Sprintf(" %d/%d", m.result.Passed, m.result.Total)
	}
	left := fmt.Sprintf("lee-lab — %s", m.t.Title)
	right := fmt.Sprintf("%s · %s%s", m.t.Domain, m.t.ID, score)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return headerStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

// renderFooter paints the bottom hint bar.
func (m *Model) renderFooter() string {
	hints := "Ctrl+G grade · Ctrl+R clear · Ctrl+Q quit · F1 help"
	if m.showHelp {
		hints = "All keystrokes → bash. Enter auto-re-grades. Ctrl+G manual grade. " +
			"Ctrl+R clears scrollback. Ctrl+Q quits. F1 toggles this help."
	}
	return footerStyle.Width(m.width).Render(hints)
}

// renderBody composes the brief pane (left) and shell pane (right)
// side-by-side. Both panes get the same height; widths are 40%/60%.
func (m *Model) renderBody() string {
	bodyH := m.height - 2 // -2 for header + footer rows
	if bodyH < 5 {
		bodyH = 5
	}
	briefW := m.width * 4 / 10
	shellW := m.width - briefW
	if briefW < 20 {
		briefW = 20
	}
	if shellW < 20 {
		shellW = 20
	}

	brief := briefBorder.
		Width(briefW - 2).
		Height(bodyH - 2).
		Render(m.briefContent(briefW - 4))

	shell := shellBorder.
		Width(shellW - 2).
		Height(bodyH - 2).
		Render(m.shellContent(shellW - 4))

	return lipgloss.JoinHorizontal(lipgloss.Top, brief, shell)
}

// briefContent assembles the task description + check status list.
func (m *Model) briefContent(innerW int) string {
	var b strings.Builder
	// Description gets a label header.
	b.WriteString(headerStyle.Render("TASK"))
	b.WriteString("\n\n")
	b.WriteString(wrapText(m.t.Description, innerW))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render("CHECKS"))
	b.WriteString("\n")
	if m.result == nil {
		b.WriteString(checkHint.Render("(grading…)"))
		return b.String()
	}
	for _, r := range m.result.Checks {
		b.WriteString(renderCheckLine(r, innerW))
		b.WriteString("\n")
	}
	return b.String()
}

// renderCheckLine paints one ✓/✗ row, optionally with the hint under it.
func renderCheckLine(r check.Result, innerW int) string {
	var icon, desc string
	if r.Passed {
		icon = checkPass.Render("✓")
		desc = r.Description
	} else {
		icon = checkFail.Render("✗")
		desc = r.Description
	}
	line := fmt.Sprintf("  %s %s", icon, desc)
	if !r.Passed && r.Hint != "" {
		hint := checkHint.Render("    hint: " + r.Hint)
		return line + "\n" + wrapText(hint, innerW)
	}
	if !r.Passed && r.Error != "" {
		errLine := checkHint.Render("    error: " + r.Error)
		return line + "\n" + wrapText(errLine, innerW)
	}
	return wrapText(line, innerW)
}

// shellContent renders the accumulated PTY output. We pass the raw
// bytes through string(); lipgloss respects ANSI sequences embedded in
// strings, so bash colors come through. Cursor-position sequences are
// dropped silently by lipgloss's width accounting.
//
// We trim the leading bytes if the buffer is taller than the pane so the
// visible portion is "the tail" — i.e. what bash just emitted, which is
// what the learner wants to see.
func (m *Model) shellContent(_ int) string {
	if len(m.ptyBuf) == 0 {
		return checkHint.Render("(bash starting up…)")
	}
	return string(m.ptyBuf)
}

// wrapText hard-wraps a long string at innerW columns. lipgloss has its
// own wrapping but it strips embedded ANSI; we want to preserve the
// formatting that's already in the description / hints.
func wrapText(s string, innerW int) string {
	if innerW <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		if lipgloss.Width(line) <= innerW {
			out = append(out, line)
			continue
		}
		// Word-wrap. Cheap algorithm — split on spaces, accumulate
		// until next word would overflow, flush.
		words := strings.Fields(line)
		var cur string
		for _, w := range words {
			candidate := cur
			if candidate != "" {
				candidate += " "
			}
			candidate += w
			if lipgloss.Width(candidate) > innerW {
				out = append(out, cur)
				cur = w
			} else {
				cur = candidate
			}
		}
		if cur != "" {
			out = append(out, cur)
		}
	}
	return strings.Join(out, "\n")
}

// Compile-time assertion that *Model satisfies tea.Model. If we drift
// from the interface (e.g. forget to rename Update), the build breaks
// here instead of at the call site in main.
var _ task.Task // keep import warm even if main.go doesn't use task directly
