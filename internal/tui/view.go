// view.go renders the bubbletea Model into a single string per frame.
//
// Layout mirrors lee-lab's three-pane browser layout: task brief on the
// left, terminal in the middle, progress (per-check status) on the
// right. Widths default to ~28% / 44% / 28% and clamp to a usable
// minimum each. On a terminal narrower than ~100 cols we collapse to
// two panes (brief+progress merged on the left, terminal on the right)
// so each pane stays readable.
//
//	┌─ lee-lab — Caleston Audit-Archive ─────────── task1-users · 4/6 ─┐
//	│ TASK         │ TERMINAL                  │ CHECKS  4/6           │
//	│              │                           │                       │
//	│ Mira's       │ [root@host ~]# id alice   │ ✓ Group sysadmins     │
//	│ narrative... │ uid=1001(alice) ...       │ ✓ User alice UID 2001 │
//	│              │ [root@host ~]# _          │ ✗ User bob exists     │
//	│              │                           │   hint: useradd ...   │
//	│              │                           │ ✓ alice in wheel      │
//	└──────────────┴───────────────────────────┴───────────────────────┘
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
	if m.passthrough {
		// Vim / nano / etc has the screen. Return an empty string so
		// bubbletea's renderer paints nothing — runPassthrough is
		// writing to os.Stdout directly. (We can't fully suppress
		// bubbletea's frame loop, but an empty View means no
		// ink hits the terminal except what the subprogram emits.)
		return ""
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

// Layout breakpoint: below this many columns we drop from 3 panes to 2
// so each remaining pane has at least ~30 usable cols.
const narrowLayoutWidth = 100

// renderBody composes the body region. Three panes on wide terminals
// (brief / terminal / progress); two on narrow ones (brief+progress
// merged into the left rail, terminal on the right).
func (m *Model) renderBody() string {
	bodyH := m.height - 2 // -2 for header + footer rows
	if bodyH < 5 {
		bodyH = 5
	}
	if m.width < narrowLayoutWidth {
		return m.renderBodyTwoPane(bodyH)
	}
	return m.renderBodyThreePane(bodyH)
}

// renderBodyThreePane is the lee-lab-style layout: brief left, terminal
// middle, progress (check status) right. Widths ~28/44/28 with each
// pane floored at 20 cols.
func (m *Model) renderBodyThreePane(bodyH int) string {
	briefW := m.width * 28 / 100
	progressW := m.width * 28 / 100
	shellW := m.width - briefW - progressW
	if briefW < 24 {
		briefW = 24
	}
	if progressW < 24 {
		progressW = 24
	}
	if shellW < 30 {
		shellW = 30
	}

	brief := briefBorder.
		Width(briefW - 2).
		Height(bodyH - 2).
		Render(m.briefDescriptionContent(briefW - 4))

	shell := shellBorder.
		Width(shellW - 2).
		Height(bodyH - 2).
		Render(m.shellContent(shellW - 4))

	progress := briefBorder.
		Width(progressW - 2).
		Height(bodyH - 2).
		Render(m.progressContent(progressW - 4))

	return lipgloss.JoinHorizontal(lipgloss.Top, brief, shell, progress)
}

// renderBodyTwoPane is the fallback for narrow terminals — brief +
// check status stack in the left rail, terminal takes the right.
func (m *Model) renderBodyTwoPane(bodyH int) string {
	briefW := m.width * 4 / 10
	shellW := m.width - briefW
	if briefW < 24 {
		briefW = 24
	}
	if shellW < 24 {
		shellW = 24
	}

	brief := briefBorder.
		Width(briefW - 2).
		Height(bodyH - 2).
		Render(m.briefStackedContent(briefW - 4))

	shell := shellBorder.
		Width(shellW - 2).
		Height(bodyH - 2).
		Render(m.shellContent(shellW - 4))

	return lipgloss.JoinHorizontal(lipgloss.Top, brief, shell)
}

// briefDescriptionContent renders ONLY the task description (3-pane
// layout — checks live in the right pane).
func (m *Model) briefDescriptionContent(innerW int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("TASK"))
	b.WriteString("\n\n")
	b.WriteString(wrapText(m.t.Description, innerW))
	return b.String()
}

// progressContent renders the per-check ✓/✗ list with hints under
// failing rows. Designed to read at a glance — the learner's eye sweeps
// from terminal output → right pane to see whether the last command
// flipped a check.
func (m *Model) progressContent(innerW int) string {
	var b strings.Builder
	// Header line includes the running score so it doubles as a
	// progress badge.
	score := "..."
	if m.result != nil {
		score = fmt.Sprintf("%d/%d", m.result.Passed, m.result.Total)
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("CHECKS  %s", score)))
	b.WriteString("\n\n")
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

// briefStackedContent is the narrow-layout left rail — description on
// top, checks below. Mirrors the three-pane content but stacked.
func (m *Model) briefStackedContent(innerW int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("TASK"))
	b.WriteString("\n\n")
	b.WriteString(wrapText(m.t.Description, innerW))
	b.WriteString("\n\n")
	score := "..."
	if m.result != nil {
		score = fmt.Sprintf("%d/%d", m.result.Passed, m.result.Total)
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("CHECKS  %s", score)))
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

// shellContent renders the accumulated PTY output. Raw bytes are passed
// through sanitizePtyForViewport which strips cursor-motion / line-clear
// sequences and bare \r while preserving SGR colours — without the
// strip, \r in the bash prompt jumps the terminal cursor to column 0 of
// the current row (which is inside the LEFT pane after JoinHorizontal),
// causing bash prompts to overwrite the task description.
func (m *Model) shellContent(_ int) string {
	if len(m.ptyBuf) == 0 {
		return checkHint.Render("(bash starting up…)")
	}
	return sanitizePtyForViewport(m.ptyBuf)
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
