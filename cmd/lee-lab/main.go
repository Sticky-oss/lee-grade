// Command lee-lab is the terminal-native sibling of the browser-based
// lee-lab simulator. It runs a real PTY bash subshell against the host's
// real filesystem, with Mira's task brief in a left-side pane and the
// shell on the right. Every typed command triggers a background re-grade
// so the per-check ✓/✗ status updates live as the learner works.
//
// Usage:
//
//	lee-lab --task tasks/rhcsa-9/task1-users.yaml
//
// Caveats compared to the browser sim:
//   - Real host — actions persist. There is no safe sandbox; if a task
//     says "useradd alice", alice ends up in /etc/passwd for real. Run on
//     a throwaway VM or container, not your laptop's primary account.
//   - Some ANSI sequences (alternate-screen-buffer, cursor save/restore)
//     don't survive the bubbletea repaint cycle — vim/less/htop will look
//     glitchy. Stick to non-fullscreen tools for the lab work.
//   - Live re-grading runs after each Enter, so the left pane's ✓/✗
//     marks the moment a check flips. No need to manually press Grade.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sticky-oss/lee-grade/internal/task"
	"github.com/sticky-oss/lee-grade/internal/tui"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	taskPath := flag.String("task", "", "path to a single task YAML file (required)")
	showVersion := flag.Bool("version", false, "print version + commit and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lee-lab %s (commit %s)\n", version, commit)
		return
	}
	if *taskPath == "" {
		fmt.Fprintln(os.Stderr, "lee-lab: --task is required")
		fmt.Fprintln(os.Stderr, "  lee-lab --task tasks/rhcsa-9/demo-host-sanity.yaml")
		os.Exit(2)
	}

	t, err := task.LoadFile(*taskPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-lab: %v\n", err)
		os.Exit(2)
	}

	m, err := tui.NewModel(t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-lab: %v\n", err)
		os.Exit(2)
	}

	// AltScreen so the TUI doesn't pollute the scrollback of the
	// terminal that launched it; on Ctrl+Q we restore the original
	// screen. MouseAllMotion is OFF — keyboard-only keeps the focus
	// model simple.
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-lab: tui error: %v\n", err)
		os.Exit(1)
	}

	// On exit, print the final grade summary to stdout so a non-TTY
	// wrapper (CI, watcher) sees the result the same way as if it had
	// run `lee-grade --task <path>`.
	if fm, ok := finalModel.(*tui.Model); ok && fm.LastResult() != nil {
		r := fm.LastResult()
		fmt.Printf("\n%s · %d/%d checks passed (%d%%)\n",
			r.TaskID, r.Passed, r.Total, r.Percent)
		if !r.FullyPassed() {
			os.Exit(1)
		}
	}
}
