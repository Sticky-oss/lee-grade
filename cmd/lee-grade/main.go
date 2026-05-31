// lee-grade — RHCSA/RHCE task grader for real RHEL boxes.
//
// Reads task definitions from a YAML file (or a directory of them) and
// grades the current host's state against each task's declarative checks.
// Same task DSL as lee-lab uses for its simulator, so a task graded by
// lee-grade on a real Rocky 9 box maps one-to-one to what the learner
// practiced in the browser sim.
//
// Usage:
//
//	lee-grade --task tasks/rhcsa-9/task1-users.yaml
//	lee-grade --tasks-dir tasks/rhcsa-9 --json
//	lee-grade --task task1-users.yaml --quiet  # exit code only
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sticky-oss/lee-grade/internal/check"
	"github.com/sticky-oss/lee-grade/internal/render"
	"github.com/sticky-oss/lee-grade/internal/task"
)

// Set at build time via -ldflags so `lee-grade --version` shows the real
// build identity. Defaults are dev placeholders.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// Subcommand dispatch must precede flag parsing: `lee-grade exam ...`
	// owns its own argument grammar (start/status/grade/reset).
	if len(os.Args) > 1 && os.Args[1] == "exam" {
		os.Exit(runExam(os.Args[2:]))
	}

	taskPath := flag.String("task", "", "path to a single task YAML file")
	tasksDir := flag.String("tasks-dir", "", "directory of task YAML files to grade recursively")
	jsonOut := flag.Bool("json", false, "emit JSON instead of human-readable output")
	quiet := flag.Bool("quiet", false, "suppress all output — communicate via exit code only")
	noColor := flag.Bool("no-color", false, "disable ANSI colour even when output is a TTY")
	listTypes := flag.Bool("list-check-types", false, "print the alphabet of registered check types and exit")
	showVersion := flag.Bool("version", false, "print version + commit and exit")
	describe := flag.Bool("describe", false, "print the task brief (scenario + graded objectives) and exit; no grading")
	steps := flag.Bool("steps", false, "output each objective + its hint as TSV, one per line (used by lab guided)")
	progress := flag.Bool("progress", false, "print your per-task progress ledger (best/last score, attempts) and exit")
	noTeach := flag.Bool("no-teach", false, "challenge mode: grade with pass/fail glyphs and score only — no detail, why, or hint")
	hostsPath := flag.String("hosts", "", "path to a hosts YAML mapping names to SSH targets; lets checks with a 'host:' grade managed nodes remotely")
	rebootTest := flag.Bool("reboot-test", false, "grade, reboot, then re-grade to prove the config survives a reboot (root; needs --task/--tasks-dir)")
	rebootResume := flag.Bool("reboot-test-resume", false, "internal: post-boot phase of --reboot-test, invoked by the generated systemd unit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lee-grade %s (commit %s)\n", version, commit)
		return
	}
	if *listTypes {
		for _, t := range check.RegisteredTypes() {
			fmt.Println(t)
		}
		return
	}
	// --progress reads the ledger; it needs no task target.
	if *progress {
		printProgress(os.Stdout, !*noColor && isTerminal(os.Stdout))
		return
	}
	// Reboot-persistence mode (v0.3). --reboot-test-resume is the hidden
	// post-boot phase the generated unit calls; --reboot-test is the
	// operator-facing driver. Both short-circuit the normal grading flow.
	if *rebootResume {
		os.Exit(resumeRebootTest())
	}
	if *rebootTest {
		os.Exit(runRebootTest(*taskPath, *tasksDir, *noColor, *jsonOut, *quiet))
	}
	if *taskPath == "" && *tasksDir == "" {
		// Bare `lee-grade` with no flags on a TTY → show the friendly
		// startup banner so the user discovers what's installed instead
		// of an error. Anywhere else (piped, scripted, --quiet, --json)
		// fall through to the usage error so automation isn't surprised.
		diagnoseTTY()
		if shouldShowBanner(*taskPath, *tasksDir, *jsonOut, *quiet) {
			// Color + animation both gated on !--no-color so users who
			// dislike one disable both with a single flag.
			useColor := !*noColor
			printBanner(os.Stdout, useColor, useColor)
			return
		}
		fmt.Fprintln(os.Stderr, "lee-grade: one of --task or --tasks-dir is required")
		fmt.Fprintln(os.Stderr, "  lee-grade --help  for full usage")
		os.Exit(2)
	}

	// Colour is on iff stdout is a TTY AND the user didn't disable it.
	render.AnsiSupported = !*noColor && !*jsonOut && isTerminal(os.Stdout)
	// --no-teach suppresses the coaching lines, turning a grade into a self-test.
	render.ShowTeaching = !*noTeach

	if *hostsPath != "" {
		if err := loadHosts(*hostsPath); err != nil {
			fmt.Fprintf(os.Stderr, "lee-grade: %v\n", err)
			os.Exit(2)
		}
	}

	tasks, code := loadTasks(*taskPath, *tasksDir)
	if code != 0 {
		os.Exit(code)
	}

	// --describe short-circuits grading: just print each task's brief. Use the
	// single render.AnsiSupported value (already gated on --no-color, --json and
	// TTY) so ANSI never leaks into a --json invocation.
	if *describe {
		color := render.AnsiSupported
		for i, t := range tasks {
			if i > 0 {
				fmt.Println()
			}
			describeBrief(os.Stdout, t, color)
		}
		return
	}

	// --steps emits objective<TAB>why<TAB>hint as TSV for the guided stepper.
	if *steps {
		for _, t := range tasks {
			for i := range t.Checks {
				c := &t.Checks[i]
				fmt.Printf("%s\t%s\t%s\n", c.Description, c.Why, c.Hint)
			}
		}
		return
	}

	// Run each task; track aggregate pass/fail so exit code reflects the
	// fleet outcome (0 iff every task fully passed).
	allPassed := true
	for _, t := range tasks {
		tr := check.RunTask(t)
		if !tr.FullyPassed() {
			allPassed = false
		}
		// Fold the result into the progress ledger (best-effort; never fatal).
		recordResult(t.ID, t.Title, tr.Passed, tr.Total, tr.Percent)
		if *quiet {
			continue
		}
		if *jsonOut {
			if err := render.JSON(os.Stdout, tr); err != nil {
				fmt.Fprintf(os.Stderr, "lee-grade: json render: %v\n", err)
			}
			continue
		}
		render.Human(os.Stdout, tr)
		fmt.Println()
	}

	if !allPassed {
		os.Exit(1)
	}
}

// loadTasks resolves --task / --tasks-dir into a task slice, shared by the
// normal grading flow and --reboot-test. On a fatal load error it returns
// (nil, exitCode); otherwise (tasks, 0). Per-file errors from a --tasks-dir
// scan are printed but non-fatal unless they leave zero tasks loaded.
func loadTasks(taskPath, tasksDir string) ([]*task.Task, int) {
	var tasks []*task.Task
	if taskPath != "" {
		t, err := task.LoadFile(taskPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lee-grade: %v\n", err)
			return nil, 2
		}
		tasks = append(tasks, t)
	}
	if tasksDir != "" {
		ts, errs := task.LoadDir(tasksDir)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "lee-grade: %v\n", e)
		}
		tasks = append(tasks, ts...)
		if len(ts) == 0 && len(errs) > 0 {
			return nil, 2
		}
	}
	return tasks, 0
}

// isTerminal is a minimal stand-in for golang.org/x/term that avoids
// pulling in the dependency for one boolean. Works for stdout / stderr
// in the contexts lee-grade actually runs.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
