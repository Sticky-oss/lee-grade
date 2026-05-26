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
func bAmber(c bool) string     { if c { return "\x1b[33m" }; return "" } // industrial / warehouse amber
func bAmberBold(c bool) string { if c { return "\x1b[1;33m" }; return "" }
func bGreen(c bool) string     { if c { return "\x1b[32m" }; return "" } // phosphor green status
func bGreenBold(c bool) string { if c { return "\x1b[1;32m" }; return "" }
func bDim(c bool) string       { if c { return "\x1b[90m" }; return "" } // slate dim, for separators / prefixes
func bWhite(c bool) string     { if c { return "\x1b[37m" }; return "" }
func bReset(c bool) string     { if c { return "\x1b[0m" }; return "" }

// Separator line is intentionally pure-ASCII (`=` not `═`) so it renders
// identically in legacy Windows conhost / cmd.exe / xterm / tmux / iTerm.
// 73 characters wide — fits an 80-column terminal with a touch of margin.
const sepLine = "========================================================================="

// bootStep is one line of the simulated boot sequence. The animation
// prints the prefix instantly, animates a row of dots over `dotMs`
// milliseconds, then prints `status` + optional `extra`.
type bootStep struct {
	elapsed     string        // simulated boot-timer reading, e.g. "0.143"
	description string        // what the step is doing
	status      string        // "OK" / "" — empty means "no status, this is the last line"
	extra       string        // optional trailing detail like "8 modules"
	dotMs       time.Duration // total time spent animating the dots
}

// printBanner writes the Caleston Logistics startup banner to w. Themed
// to feel like booting an industrial logistics company's audit terminal —
// matches the lee-lab narrative framing (Mira Okafor / Caleston Logistics
// / audit-archive.caleston.internal).
//
//	color   — emit ANSI escapes; false on --no-color or non-TTY.
//	animate — pause between boot lines for the typewriter / CRT-warmup
//	          feel; false on --no-color, non-TTY, or in tests.
//
// Uses ONLY simple printing + sleep — no cursor movement (`\b`, `\r`,
// ESC[K), no Unicode glyphs beyond ASCII. That's the compatibility key:
// legacy Windows PowerShell / cmd.exe / conhost render this identically
// to Windows Terminal, GNOME Terminal, and tmux.
func printBanner(w io.Writer, color, animate bool) {
	// ─── Header ──────────────────────────────────────────────────────
	fmt.Fprintf(w, "%s%s%s\n", bDim(color), sepLine, bReset(color))
	fmt.Fprintf(w, "  %sCALESTON LOGISTICS%s%s  -  TRANSPORT & WAREHOUSING  -  est. 1987%s\n",
		bAmberBold(color), bReset(color), bAmber(color), bReset(color))
	fmt.Fprintf(w, "  %sAudit Archive  -  Operations Terminal  -  /var/audit%s\n",
		bDim(color), bReset(color))
	fmt.Fprintf(w, "%s%s%s\n", bDim(color), sepLine, bReset(color))
	fmt.Fprintln(w)
	flush(w)

	if animate {
		time.Sleep(180 * time.Millisecond)
	}

	// ─── Boot sequence ───────────────────────────────────────────────
	steps := []bootStep{
		{"0.001", "firmware self-test", "OK", "", 120 * time.Millisecond},
		{"0.143", "mounting /var/audit", "OK", "", 180 * time.Millisecond},
		{"0.287", "loading task definitions", "OK", "", 260 * time.Millisecond},
		{"0.412", "verifying check engine", "OK",
			fmt.Sprintf("%d modules", len(check.RegisteredTypes())),
			220 * time.Millisecond},
		{"0.501", "terminal ready", "", "", 80 * time.Millisecond},
	}
	for _, s := range steps {
		printBootStep(w, s, color, animate)
	}

	fmt.Fprintln(w)
	flush(w)

	if animate {
		time.Sleep(150 * time.Millisecond)
	}

	// ─── Operator block (the Mira Okafor / Caleston narrative anchor) ─
	fmt.Fprintf(w, "  %sOperator   :%s %smira.okafor@audit-archive%s\n",
		bDim(color), bReset(color), bGreenBold(color), bReset(color))
	fmt.Fprintf(w, "  %sBuild      :%s %slee-grade v%s%s  %s(real-host grading mode)%s\n",
		bDim(color), bReset(color), bGreen(color), version, bReset(color),
		bDim(color), bReset(color))
	fmt.Fprintln(w)
	flush(w)

	if animate {
		time.Sleep(120 * time.Millisecond)
	}

	// ─── Footer + command hints ──────────────────────────────────────
	fmt.Fprintf(w, "%s%s%s\n", bDim(color), sepLine, bReset(color))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Type:  %slee-grade --task <task.yaml>%s    begin audit\n",
		bAmber(color), bReset(color))
	fmt.Fprintf(w, "         %slee-grade --tasks-dir <dir>%s     grade a directory\n",
		bAmber(color), bReset(color))
	fmt.Fprintf(w, "         %slee-grade --help%s                full command reference\n",
		bAmber(color), bReset(color))
	fmt.Fprintln(w)
	flush(w)
}

// printBootStep prints one boot line with progressive dots between the
// description and the OK status. Pure forward-only output — no cursor
// tricks — so it renders identically in any terminal that handles \n.
//
// Layout (column-aligned):
//
//	[ X.XXXs ] description ...... OK   extra
//
// Total target width before "OK" is 60 columns; we pad with dots to hit
// that, then write the status. Aligning the OKs makes the sequence read
// as a "real" boot log even though every line is the same height.
func printBootStep(w io.Writer, s bootStep, color, animate bool) {
	prefix := fmt.Sprintf("[ %ss ] %s ", s.elapsed, s.description)
	fmt.Fprint(w, bDim(color))
	fmt.Fprint(w, prefix)
	fmt.Fprint(w, bReset(color))
	flush(w)

	// Pad to 60 cols with dots, animated when in TTY mode.
	const target = 60
	dotsNeeded := target - len(prefix)
	if dotsNeeded < 3 {
		dotsNeeded = 3
	}
	if animate && s.dotMs > 0 {
		per := s.dotMs / time.Duration(dotsNeeded)
		fmt.Fprint(w, bDim(color))
		for i := 0; i < dotsNeeded; i++ {
			fmt.Fprint(w, ".")
			flush(w)
			time.Sleep(per)
		}
		fmt.Fprint(w, bReset(color))
	} else {
		fmt.Fprintf(w, "%s%s%s", bDim(color), strings.Repeat(".", dotsNeeded), bReset(color))
	}

	if s.status != "" {
		fmt.Fprintf(w, " %s%s%s", bGreenBold(color), s.status, bReset(color))
	}
	if s.extra != "" {
		fmt.Fprintf(w, "   %s%s%s", bDim(color), s.extra, bReset(color))
	}
	fmt.Fprintln(w)
	flush(w)

	if animate {
		// Brief inter-step pause so boot lines don't feel rushed.
		time.Sleep(60 * time.Millisecond)
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

// flush forces the underlying writer to push bytes if it's a *os.File
// (the only writer the CLI uses today). Without this, the animation
// frames can buffer up and arrive all at once — defeating the whole
// "boot sequence" effect.
func flush(w io.Writer) {
	if f, ok := w.(*os.File); ok {
		_ = f.Sync()
	}
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
