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
	taskPath := flag.String("task", "", "path to a single task YAML file")
	tasksDir := flag.String("tasks-dir", "", "directory of task YAML files to grade recursively")
	jsonOut := flag.Bool("json", false, "emit JSON instead of human-readable output")
	quiet := flag.Bool("quiet", false, "suppress all output — communicate via exit code only")
	noColor := flag.Bool("no-color", false, "disable ANSI colour even when output is a TTY")
	listTypes := flag.Bool("list-check-types", false, "print the alphabet of registered check types and exit")
	showVersion := flag.Bool("version", false, "print version + commit and exit")
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
	if *taskPath == "" && *tasksDir == "" {
		fmt.Fprintln(os.Stderr, "lee-grade: one of --task or --tasks-dir is required")
		fmt.Fprintln(os.Stderr, "  lee-grade --help  for full usage")
		os.Exit(2)
	}

	// Colour is on iff stdout is a TTY AND the user didn't disable it.
	render.AnsiSupported = !*noColor && !*jsonOut && isTerminal(os.Stdout)

	var tasks []*task.Task
	if *taskPath != "" {
		t, err := task.LoadFile(*taskPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lee-grade: %v\n", err)
			os.Exit(2)
		}
		tasks = append(tasks, t)
	}
	if *tasksDir != "" {
		ts, errs := task.LoadDir(*tasksDir)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "lee-grade: %v\n", e)
		}
		tasks = append(tasks, ts...)
		if len(ts) == 0 && len(errs) > 0 {
			os.Exit(2)
		}
	}

	// Run each task; track aggregate pass/fail so exit code reflects the
	// fleet outcome (0 iff every task fully passed).
	allPassed := true
	for _, t := range tasks {
		tr := check.RunTask(t)
		if !tr.FullyPassed() {
			allPassed = false
		}
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
