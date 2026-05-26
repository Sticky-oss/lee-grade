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

// logo is intentionally six lines tall — fits even a short terminal window
// without scrolling the banner off-screen before it finishes animating.
var logo = []string{
	"  __                                __     ",
	" / /  ___  ___  ____ __________ ___/ /__   ",
	"/ /__/ -_)/ -_)/ _ `/ __/ _ `/ _  / -_)   ",
	"\\____/\\__/ \\__/ \\_, /_/  \\_,_/\\_,_/\\__/    ",
	"                /___/                       ",
}

// printBanner writes the animated startup banner to w.
//
//	color   — emit ANSI escapes; false on --no-color or non-TTY.
//	animate — reveal one line at a time with a brief delay; false on
//	          --no-color, non-TTY, or any time we want the banner without
//	          the wait (e.g. tests).
//
// Caller decides WHEN to show the banner; this function only decides HOW.
func printBanner(w io.Writer, color, animate bool) {
	delay := 25 * time.Millisecond
	if !animate {
		delay = 0
	}

	for _, line := range logo {
		fmt.Fprintf(w, "%s%s%s\n", bAccent(color), line, bReset(color))
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	// Tagline + version on one line below the logo.
	if delay > 0 {
		time.Sleep(delay)
	}
	fmt.Fprintf(w, "%s  RHCSA / RHCE task grader%s  %sv%s%s\n",
		bDim(color), bReset(color), bAccent(color), version, bReset(color))

	if delay > 0 {
		time.Sleep(delay * 2)
	}
	fmt.Fprintf(w, "%s  Companion to lee-lab — same task DSL, real hosts.%s\n",
		bDim(color), bReset(color))
	fmt.Fprintln(w)

	// Mini boot-status: counts registered check types + flashes a brief
	// "initializing" line that resolves to "ready". Total budget ~250 ms.
	checkCount := len(check.RegisteredTypes())
	if animate {
		fmt.Fprintf(w, "  %sinitializing check engine...%s", bDim(color), bReset(color))
		for _, frame := range []string{"   ⠋", "   ⠼", "   ⠿"} {
			fmt.Fprint(w, frame)
			time.Sleep(60 * time.Millisecond)
			fmt.Fprint(w, "\b\b\b\b")
		}
		fmt.Fprint(w, "\r\x1b[2K")
	}
	fmt.Fprintf(w, "  %s✓ ready%s  %s%d check types registered%s\n",
		bGreen(color), bReset(color), bDim(color), checkCount, bReset(color))
	fmt.Fprintln(w)

	// Call to action — what to actually type next.
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
