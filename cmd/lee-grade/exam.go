package main

// `lee-grade exam` (v0.5): the timed-sitting harness on top of the task grader.
//
// An exam is an ordered set of tasks graded as ONE sitting against a cut score
// and a wall-clock budget — the EX200/EX294 experience. The flow is driven by
// on-disk session state under /var/lib/lee-grade so separate invocations share
// one running clock:
//
//	sudo lee-grade exam start exams/rhce-9-sample.yaml   # arms the clock, prints the brief
//	sudo lee-grade exam status                           # time remaining + objectives
//	sudo lee-grade exam grade                            # score the host vs the cut + the clock
//	sudo lee-grade exam reset                            # clear the session for a fresh attempt
//
// Scoring is partial-credit by check across all tasks; the sitting passes iff
// the aggregate clears pass_percent AND was submitted within time.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sticky-oss/lee-grade/internal/check"
	"github.com/sticky-oss/lee-grade/internal/exam"
	"github.com/sticky-oss/lee-grade/internal/render"
	"github.com/sticky-oss/lee-grade/internal/task"
)

const (
	examStateFile  = "/var/lib/lee-grade/exam-state.json"
	examReportFile = "/var/lib/lee-grade/exam-report.json"
)

// examReport is the persisted/serialized grade of a sitting.
type examReport struct {
	Version     int        `json:"version"`
	ExamID      string     `json:"exam_id"`
	Title       string     `json:"title"`
	Track       string     `json:"track,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	Deadline    time.Time  `json:"deadline"`
	GradedAt    time.Time  `json:"graded_at"`
	WithinTime  bool       `json:"within_time"`
	PassPercent int        `json:"pass_percent"`
	Score       exam.Score `json:"score"`
}

// runExam dispatches `lee-grade exam <subcommand>`.
func runExam(args []string) int {
	if len(args) == 0 {
		examUsage(os.Stderr)
		return 2
	}
	// Colour on for the start/status/reset paths when stdout is a TTY; the
	// grade path re-derives this from its own --no-color/--json flags.
	render.AnsiSupported = isTerminal(os.Stdout)
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return examStart(rest)
	case "status":
		return examStatus(rest)
	case "grade", "submit":
		return examGrade(rest)
	case "reset":
		return examReset(rest)
	case "-h", "--help", "help":
		examUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "lee-grade exam: unknown subcommand %q\n", sub)
		examUsage(os.Stderr)
		return 2
	}
}

func examUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: lee-grade exam <start|status|grade|reset>")
	fmt.Fprintln(w, "  start <exam.yaml>   begin a timed sitting (arms the clock, prints the brief)")
	fmt.Fprintln(w, "  status              show time remaining and the objectives")
	fmt.Fprintln(w, "  grade               score the host against the cut score and the clock")
	fmt.Fprintln(w, "  reset               clear the session so a fresh sitting can start")
}

// examStart loads + validates the exam (and every referenced task), then arms
// the session clock. Refuses to clobber an in-progress sitting.
func examStart(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "lee-grade exam start: needs an exam file, e.g. exams/rhce-9-sample.yaml")
		return 2
	}
	examPath := args[0]

	if fileExists(examStateFile) {
		st, _ := readExamState()
		fmt.Fprintf(os.Stderr, "lee-grade: a sitting for %q is already in progress (started %s).\n",
			st.ExamID, st.StartedAt.Local().Format(time.RFC3339))
		fmt.Fprintln(os.Stderr, "  Run `lee-grade exam reset` first to start over.")
		return 2
	}

	e, err := exam.LoadExam(examPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: %v\n", err)
		return 2
	}
	tasks, err := loadTaskPaths(e.Tasks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: exam %q references a task that won't load: %v\n", e.ID, err)
		return 2
	}

	st := exam.NewState(e, examPath, time.Now())
	if err := os.MkdirAll(rebootStateDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot create %s: %v\n", rebootStateDir, err)
		return 1
	}
	if err := writeJSONFile(examStateFile, st); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot save exam state: %v\n", err)
		return 1
	}

	color := render.AnsiSupported
	printExamBrief(os.Stdout, st, tasks, color)
	return 0
}

// examStatus reports the time left and the objectives without grading.
func examStatus(args []string) int {
	st, err := readExamState()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lee-grade: no exam in progress (start one with `lee-grade exam start <exam.yaml>`)")
		return 2
	}
	color := render.AnsiSupported
	now := time.Now()
	fmt.Printf("== %s ==\n", st.Title)
	printClock(os.Stdout, st, now, color)
	fmt.Println()
	tasks, _ := loadTaskPaths(st.TaskPaths) // best-effort titles
	fmt.Println("Objectives:")
	for i, p := range st.TaskPaths {
		title, domain := p, ""
		if i < len(tasks) && tasks[i] != nil {
			title, domain = tasks[i].Title, tasks[i].Domain
		}
		if domain != "" {
			fmt.Printf("  %d. %s  (%s)\n", i+1, title, domain)
		} else {
			fmt.Printf("  %d. %s\n", i+1, title)
		}
	}
	fmt.Println()
	fmt.Println("Submit with: lee-grade exam grade")
	return 0
}

// examGrade scores every task against current host state, compares the
// aggregate to the cut score and the clock, and writes a report. Exit 0 iff
// the sitting both clears the cut AND was submitted within time.
func examGrade(args []string) int {
	fs := flag.NewFlagSet("exam grade", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON instead of human-readable output")
	noColor := fs.Bool("no-color", false, "disable ANSI colour")
	quiet := fs.Bool("quiet", false, "suppress per-task output; print only the summary")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := readExamState()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lee-grade: no exam in progress (start one with `lee-grade exam start <exam.yaml>`)")
		return 2
	}
	tasks, err := loadTaskPaths(st.TaskPaths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot reload exam tasks: %v\n", err)
		return 1
	}

	render.AnsiSupported = !*noColor && !*jsonOut && isTerminal(os.Stdout)
	color := render.AnsiSupported

	now := time.Now()
	within := st.WithinTime(now)

	outcomes := make([]exam.TaskOutcome, 0, len(tasks))
	for _, t := range tasks {
		tr := check.RunTask(t)
		outcomes = append(outcomes, exam.TaskOutcome{
			TaskID: tr.TaskID, Title: tr.Title, Domain: tr.Domain,
			Passed: tr.Passed, Total: tr.Total,
		})
		if !*quiet && !*jsonOut {
			render.Human(os.Stdout, tr)
			fmt.Println()
		}
	}
	score := exam.ScoreExam(outcomes, st.PassPercent)

	rep := examReport{
		Version: 1, ExamID: st.ExamID, Title: st.Title, Track: st.Track,
		StartedAt: st.StartedAt, Deadline: st.Deadline, GradedAt: now.UTC(),
		WithinTime: within, PassPercent: st.PassPercent, Score: score,
	}
	if err := writeJSONFile(examReportFile, rep); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot write exam report: %v\n", err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		// The scorecard is the point of grading, so it prints even with
		// --quiet; --quiet only suppresses the per-task detail boxes above.
		printExamSummary(os.Stdout, st, score, within, now, color)
	}

	if score.Passed && within {
		return 0
	}
	return 1
}

// examReset clears the session so a fresh sitting can begin. Host state is NOT
// restored — that is a separate concern (VM snapshot, or per-task cleanup).
func examReset(args []string) int {
	removedState := os.Remove(examStateFile) == nil
	_ = os.Remove(examReportFile)
	if removedState {
		fmt.Println("Exam session cleared — `lee-grade exam start <exam.yaml>` to begin a fresh sitting.")
	} else {
		fmt.Println("No exam session to clear.")
	}
	fmt.Println("Note: this does not restore host state. Snapshot the VM (or re-run per-task cleanup) for a clean retake.")
	return 0
}

// loadTaskPaths loads each task file, stopping at the first error.
func loadTaskPaths(paths []string) ([]*task.Task, error) {
	tasks := make([]*task.Task, 0, len(paths))
	for _, p := range paths {
		t, err := task.LoadFile(p)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func readExamState() (exam.State, error) {
	var st exam.State
	b, err := os.ReadFile(examStateFile)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// printExamBrief shows the exam at the start of a sitting.
func printExamBrief(w io.Writer, st exam.State, tasks []*task.Task, color bool) {
	const blue, dim, green = "\x1b[34m", "\x1b[90m", "\x1b[32m"
	fmt.Fprintln(w, col(color, blue, "══ "+st.Title+" — sitting started ══"))
	fmt.Fprintf(w, "   time budget %s · cut score %d%% · %d task(s)\n",
		formatDuration(st.Deadline.Sub(st.StartedAt)), st.PassPercent, len(st.TaskPaths))
	fmt.Fprintf(w, "   %s\n\n", col(color, dim, "deadline "+st.Deadline.Local().Format("Mon 15:04:05 MST")))
	fmt.Fprintln(w, "Objectives:")
	for i, t := range tasks {
		dom := ""
		if t.Domain != "" {
			dom = "  " + col(color, dim, "("+t.Domain+")")
		}
		fmt.Fprintf(w, "  %d. %s%s\n", i+1, t.Title, dom)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, col(color, green, "The clock is running.")+" Do the work, then: lee-grade exam grade")
}

// printClock renders the remaining-time line (or the overtime line).
func printClock(w io.Writer, st exam.State, now time.Time, color bool) {
	const green, red, dim = "\x1b[32m", "\x1b[31m", "\x1b[90m"
	rem := st.Remaining(now)
	if rem >= 0 {
		fmt.Fprintf(w, "   %s remaining   %s\n",
			col(color, green, formatDuration(rem)),
			col(color, dim, "deadline "+st.Deadline.Local().Format("15:04:05 MST")))
	} else {
		fmt.Fprintf(w, "   %s   %s\n",
			col(color, red, "OVERTIME by "+formatDuration(-rem)),
			col(color, dim, "deadline was "+st.Deadline.Local().Format("15:04:05 MST")))
	}
}

// printExamSummary renders the boxed final scorecard.
func printExamSummary(w io.Writer, st exam.State, score exam.Score, within bool, now time.Time, color bool) {
	const blue, dim, green, red, yellow = "\x1b[34m", "\x1b[90m", "\x1b[32m", "\x1b[31m", "\x1b[33m"
	header := fmt.Sprintf("Exam %s · %s", st.ExamID, st.Title)
	fmt.Fprintln(w, col(color, blue, "┌─ "+header+" "+strings.Repeat("─", max(0, 70-3-len(header)))+"┐"))
	for _, ts := range score.Tasks {
		glyph, code := "✗", red
		if ts.Total > 0 && ts.Passed == ts.Total {
			glyph, code = "✓", green
		} else if ts.Passed > 0 {
			glyph, code = "~", yellow
		}
		line := fmt.Sprintf("%s %-44s %d/%d", col(color, code, glyph), truncate(ts.Title, 44), ts.Passed, ts.Total)
		fmt.Fprintln(w, col(color, blue, "│ ")+line)
	}
	fmt.Fprintln(w, col(color, blue, "│"))

	scoreCode := red
	if score.Passed {
		scoreCode = green
	}
	fmt.Fprintln(w, col(color, blue, "│ ")+fmt.Sprintf("score: %s  (%d/%d checks, cut %d%%)",
		col(color, scoreCode, fmt.Sprintf("%d%%", score.Percent)), score.PassedChecks, score.TotalChecks, score.PassPercent))

	timeCode, timeMsg := green, "submitted within time"
	if !within {
		timeCode, timeMsg = red, "submitted in OVERTIME (would not count on exam day)"
	}
	fmt.Fprintln(w, col(color, blue, "│ ")+"time:  "+col(color, timeCode, timeMsg))
	fmt.Fprintln(w, col(color, blue, "└"+strings.Repeat("─", 69)+"┘"))

	switch {
	case score.Passed && within:
		fmt.Fprintln(w, col(color, green, fmt.Sprintf("PASS — %d%% ≥ %d%%, within time.", score.Percent, score.PassPercent)))
	case score.Passed && !within:
		fmt.Fprintln(w, col(color, yellow, fmt.Sprintf("Score clears the cut (%d%%) but the sitting ran over — on exam day this would not count.", score.Percent)))
	default:
		fmt.Fprintln(w, col(color, red, fmt.Sprintf("FAIL — %d%% < %d%% cut score.", score.Percent, score.PassPercent)))
	}
}

// formatDuration renders a duration as e.g. "1h23m" / "0h45m" / "12m04s",
// rounded to whole seconds. Used for the clock display.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
