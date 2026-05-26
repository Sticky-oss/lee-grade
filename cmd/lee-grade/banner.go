package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sticky-oss/lee-grade/internal/check"
)

// ANSI helpers — narrowed copy so banner stays self-contained.
// Each helper takes the local `color` flag and returns either the escape
// sequence or an empty string. Threading the flag everywhere keeps the
// caller side small (no global to set/restore).
func bAccent(c bool) string { if c { return "\x1b[94m" }; return "" } // muted blue
func bDim(c bool) string    { if c { return "\x1b[90m" }; return "" } // slate dim
func bAmber(c bool) string  { if c { return "\x1b[33m" }; return "" } // call-to-action amber
func bGreen(c bool) string  { if c { return "\x1b[32m" }; return "" } // "ready" badge
func bReset(c bool) string  { if c { return "\x1b[0m" }; return "" }

// logo is intentionally five lines tall — fits even a short terminal
// window without scrolling the banner off-screen before it finishes
// animating. ASCII-only so it renders in every terminal (Windows
// conhost, GNOME Terminal, iTerm, tmux, etc.) without needing nerd
// fonts or unicode support.
var logo = []string{
	"  _                                       _      ",
	" | | ___  ___        __ _ _ __ __ _  __ _| | ___ ",
	" | |/ _ \\/ _ \\_____ / _` | '__/ _` |/ _` | |/ _ \\",
	" | |  __/  __/_____| (_| | | | (_| | (_| | |  __/",
	" |_|\\___|\\___|      \\__, |_|  \\__,_|\\__,_|_|\\___|",
	"                    |___/                         ",
}

// printBanner writes the animated startup banner to w.
//
//	color   — emit ANSI escapes; false on --no-color or non-TTY.
//	animate — reveal one line at a time with a brief delay; false on
//	          --no-color, non-TTY, or any time we want the banner without
//	          the wait (e.g. tests).
//
// Caller decides WHEN to show the banner; this function only decides HOW.
//
// Output is flushed line-by-line via the OS's stdout (unbuffered) so the
// reveal animation actually shows up in real terminals rather than
// arriving as one buffered blob at the end.
func printBanner(w io.Writer, color, animate bool) {
	logoDelay := 60 * time.Millisecond
	stepDelay := 120 * time.Millisecond
	spinnerDelay := 110 * time.Millisecond
	if !animate {
		logoDelay = 0
		stepDelay = 0
		spinnerDelay = 0
	}

	for _, line := range logo {
		fmt.Fprintf(w, "%s%s%s\n", bAccent(color), line, bReset(color))
		flush(w)
		if logoDelay > 0 {
			time.Sleep(logoDelay)
		}
	}

	if stepDelay > 0 {
		time.Sleep(stepDelay)
	}
	fmt.Fprintf(w, "%s  RHCSA / RHCE task grader%s  %sv%s%s\n",
		bDim(color), bReset(color), bAccent(color), version, bReset(color))
	flush(w)

	if stepDelay > 0 {
		time.Sleep(stepDelay)
	}
	fmt.Fprintf(w, "%s  Companion to lee-lab - same task DSL, real hosts.%s\n",
		bDim(color), bReset(color))
	fmt.Fprintln(w)
	flush(w)

	// Mini boot-status. Use the rotating-bar ASCII spinner (|/-\) so the
	// glyphs render on every terminal — earlier braille chars (⠋⠼⠿) were
	// missing from Windows conhost's default fonts and showed as blanks
	// or ?.
	checkCount := len(check.RegisteredTypes())
	if animate {
		fmt.Fprintf(w, "  %sinitializing check engine ...%s ", bDim(color), bReset(color))
		flush(w)
		for _, frame := range []rune{'|', '/', '-', '\\', '|', '/', '-', '\\'} {
			fmt.Fprintf(w, "%c", frame)
			flush(w)
			time.Sleep(spinnerDelay)
			// One backspace covers the one-cell spinner glyph exactly.
			fmt.Fprint(w, "\b")
			flush(w)
		}
		// Return to start of line + clear, then print the resolved status.
		// Two-step (CR then ESC[2K) so it works on the broadest set of
		// terminals — some old terminals don't honour ESC[2K but every
		// one honours CR + overwrite.
		fmt.Fprint(w, "\r\x1b[2K")
		flush(w)
	}
	if color {
		fmt.Fprintf(w, "  %s[OK] ready%s  %s%d check types registered%s\n",
			bGreen(color), bReset(color), bDim(color), checkCount, bReset(color))
	} else {
		fmt.Fprintf(w, "  [OK] ready  %d check types registered\n", checkCount)
	}
	fmt.Fprintln(w)
	flush(w)

	if stepDelay > 0 {
		time.Sleep(stepDelay)
	}
	fmt.Fprintf(w, "  Try:\n")
	fmt.Fprintf(w, "    %slee-grade --task <task.yaml>%s   grade one task\n",
		bAmber(color), bReset(color))
	fmt.Fprintf(w, "    %slee-grade --tasks-dir <dir>%s    grade a directory\n",
		bAmber(color), bReset(color))
	fmt.Fprintf(w, "    %slee-grade --list-check-types%s   show the check alphabet\n",
		bAmber(color), bReset(color))
	fmt.Fprintf(w, "    %slee-grade --help%s               full usage\n",
		bAmber(color), bReset(color))
	fmt.Fprintln(w)
	flush(w)
}

// flush forces the underlying writer to push bytes if it's a *os.File
// (the only writer the CLI uses today). Without this, the animation
// frames can buffer up and arrive all at once.
func flush(w io.Writer) {
	if f, ok := w.(*os.File); ok {
		_ = f.Sync()
	}
}

// shouldShowBanner returns true when invoking lee-grade with no useful
// arguments at all — the case where a banner adds value. Any flag, any
// task path, JSON mode, quiet mode, or a non-TTY stdout suppresses it.
func shouldShowBanner(taskPath, tasksDir string, jsonOut, quiet bool) bool {
	if taskPath != "" || tasksDir != "" {
		return false
	}
	if jsonOut || quiet {
		return false
	}
	return isTerminal(os.Stdout)
}

// diagnoseTTY prints what isTerminal sees on stderr. Gated by an env var
// so it's harmless in production but available for "why doesn't the
// banner show?" debugging. Run with LEE_GRADE_DEBUG_TTY=1.
func diagnoseTTY() {
	if os.Getenv("LEE_GRADE_DEBUG_TTY") == "" {
		return
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[diag] stdout.Stat error: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[diag] stdout mode=%s isCharDevice=%v isPipe=%v isTerminal=%v\n",
		fi.Mode().String(),
		fi.Mode()&os.ModeCharDevice != 0,
		fi.Mode()&os.ModeNamedPipe != 0,
		isTerminal(os.Stdout),
	)
}

// stringsRepeat is a one-line wrapper kept to limit cross-package imports
// in this tiny file. (Avoiding pulling in strings.Repeat from the strings
// package keeps the diff small if we later want to vendor banner.go.)
var _ = strings.Repeat // silence unused warning when stripAnsi is removed

// stripAnsi is a small helper so tests can compare banner output text
// without ANSI codes confusing the assertions. Not used at runtime.
func stripAnsi(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' || r == 'K' {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
