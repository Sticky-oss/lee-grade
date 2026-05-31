package main

// --reboot-test (v0.3): prove that graded configuration survives a reboot.
//
// A learner can harden a box so every check passes, yet leave the changes
// runtime-only (e.g. `firewall-cmd --add-service=http` without --permanent,
// or `setenforce 1` without editing /etc/selinux/config). Those pass a normal
// grade but silently revert on the next boot. This mode catches exactly that.
//
// The cycle is driven by on-disk state so a single command sees it through:
//
//	sudo lee-grade --task X --reboot-test   # grade now, arm, reboot
//	(box reboots; a one-shot systemd unit re-grades and writes a report)
//	sudo lee-grade --task X --reboot-test   # print the persistence verdict
//
// The post-boot phase (--reboot-test-resume) is invoked only by the generated
// unit, never by a human.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sticky-oss/lee-grade/internal/check"
	"github.com/sticky-oss/lee-grade/internal/render"
)

const (
	rebootStateDir   = "/var/lib/lee-grade"
	rebootStateFile  = "/var/lib/lee-grade/reboot-test-state.json"
	rebootReportFile = "/var/lib/lee-grade/reboot-test-report.json"
	rebootUnitName   = "lee-grade-reboot-test.service"
	rebootUnitPath   = "/etc/systemd/system/lee-grade-reboot-test.service"
	// The post-boot unit runs as systemd (init_t). Under SELinux, init_t may
	// not exec a binary carrying a home-directory label, so we stage a copy
	// into a system bin dir and relabel it bin_t before pointing the unit here.
	rebootStagedBin = "/usr/local/sbin/lee-grade-reboot-test"
)

// Per-check verdicts after diffing pre- vs post-reboot grades.
const (
	statusPersisted    = "persisted"     // passed before AND after — config is durable
	statusRegressed    = "regressed"     // passed before, failed after — did NOT survive reboot
	statusRecovered    = "recovered"     // failed before, passed after — came up on boot
	statusStillFailing = "still-failing" // failed before AND after — nothing to do with reboot
	statusUnknown      = "unknown"       // couldn't be re-evaluated after reboot
)

// rebootState is persisted before the reboot so the post-boot phase can
// reload the same tasks and diff against the pre-reboot results.
type rebootState struct {
	Version   int                 `json:"version"`
	TaskPath  string              `json:"task_path,omitempty"`
	TasksDir  string              `json:"tasks_dir,omitempty"`
	StartedAt time.Time           `json:"started_at"`
	Pre       []*check.TaskResult `json:"pre"`
}

// rebootReport is the post-boot diff, written for the operator's next run.
type rebootReport struct {
	Version     int              `json:"version"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
	Regressions int              `json:"regressions"`
	Tasks       []rebootTaskDiff `json:"tasks"`
}

type rebootTaskDiff struct {
	TaskID string            `json:"task_id"`
	Title  string            `json:"title"`
	Checks []rebootCheckDiff `json:"checks"`
}

type rebootCheckDiff struct {
	CheckID     string `json:"check_id"`
	Description string `json:"description"`
	PrePassed   bool   `json:"pre_passed"`
	PostPassed  bool   `json:"post_passed"`
	PostDetail  string `json:"post_detail,omitempty"`
	Status      string `json:"status"`
}

// runRebootTest is the operator-facing entry for --reboot-test. It picks a
// phase from on-disk state so the same command drives the whole cycle:
//
//	report present       → operator is back post-reboot; print + clean up
//	state present, no rep → post-boot grade not done yet; tell them to wait
//	neither              → fresh start: grade, arm a one-shot unit, reboot
func runRebootTest(taskPath, tasksDir string, noColor, jsonOut, quiet bool) int {
	if fileExists(rebootReportFile) {
		return showRebootReport(noColor, jsonOut, quiet)
	}
	if fileExists(rebootStateFile) {
		fmt.Fprintln(os.Stderr, "lee-grade: a reboot-test is in progress but its post-boot grade hasn't")
		fmt.Fprintln(os.Stderr, "  written a report yet. Wait a few seconds and re-run --reboot-test, or")
		fmt.Fprintln(os.Stderr, "  force the post-boot phase now: sudo lee-grade --reboot-test-resume")
		return 2
	}
	return startRebootTest(taskPath, tasksDir, noColor)
}

// startRebootTest grades the host now, saves the result, arms a self-removing
// systemd one-shot to re-grade after boot, and reboots.
func startRebootTest(taskPath, tasksDir string, noColor bool) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "lee-grade: --reboot-test must run as root (it writes /var/lib, installs a")
		fmt.Fprintln(os.Stderr, "  systemd unit, and reboots). Try: sudo lee-grade --task ... --reboot-test")
		return 2
	}
	tasks, code := loadTasks(taskPath, tasksDir)
	if code != 0 {
		return code
	}
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "lee-grade: --reboot-test needs at least one task (--task or --tasks-dir)")
		return 2
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot resolve own path: %v\n", err)
		return 1
	}
	staged, err := stageSelf(self)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot stage executable for the post-boot grade: %v\n", err)
		return 1
	}

	render.AnsiSupported = !noColor && isTerminal(os.Stdout)
	fmt.Println("== lee-grade reboot-test · PRE-REBOOT grade ==")
	fmt.Println()
	pre := make([]*check.TaskResult, 0, len(tasks))
	for _, t := range tasks {
		tr := check.RunTask(t)
		pre = append(pre, tr)
		render.Human(os.Stdout, tr)
		fmt.Println()
	}

	st := rebootState{
		Version:   1,
		TaskPath:  taskPath,
		TasksDir:  tasksDir,
		StartedAt: time.Now().UTC(),
		Pre:       pre,
	}
	if err := os.MkdirAll(rebootStateDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot create %s: %v\n", rebootStateDir, err)
		return 1
	}
	if err := writeJSONFile(rebootStateFile, st); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot save reboot-test state: %v\n", err)
		return 1
	}
	if err := installRebootUnit(staged); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot arm post-boot grade: %v\n", err)
		_ = os.Remove(rebootStateFile)
		_ = os.Remove(rebootStagedBin)
		return 1
	}

	fmt.Println("Pre-reboot grade saved; armed a one-shot systemd unit to re-grade after boot.")
	fmt.Println("Rebooting now…")
	fmt.Printf("When the box is back, run the same command to see the persistence report:\n")
	fmt.Printf("  sudo lee-grade%s --reboot-test\n", taskArgString(taskPath, tasksDir))

	if err := exec.Command("systemctl", "reboot").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: reboot command failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "  state is saved and the unit is armed; reboot manually to continue.")
		return 1
	}
	return 0
}

// resumeRebootTest is the post-boot phase, invoked by the generated systemd
// unit. It re-grades, diffs against the saved pre-reboot results, writes the
// report for the operator's next run, then disarms itself.
func resumeRebootTest() int {
	st, err := readState()
	if err != nil {
		// No readable state: nothing to do. Disarm so we don't re-run on
		// every subsequent boot.
		fmt.Fprintf(os.Stderr, "lee-grade: reboot-test resume: no state (%v); disarming\n", err)
		removeRebootUnit()
		return 0
	}
	tasks, code := loadTasks(st.TaskPath, st.TasksDir)
	if code != 0 || len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "lee-grade: reboot-test resume: cannot reload tasks; disarming")
		removeRebootUnit()
		_ = os.Remove(rebootStateFile)
		if code == 0 {
			code = 1 // empty reload is still a failure, not success
		}
		return code
	}

	post := make([]*check.TaskResult, 0, len(tasks))
	for _, t := range tasks {
		post = append(post, check.RunTask(t))
	}

	rep := diffRebootResults(st, post)
	if err := writeJSONFile(rebootReportFile, rep); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: reboot-test resume: cannot write report: %v\n", err)
	}
	// Echo to stdout so `journalctl -u lee-grade-reboot-test` shows the verdict.
	printRebootReport(os.Stdout, rep, false)

	removeRebootUnit()
	_ = os.Remove(rebootStateFile)
	return 0
}

// showRebootReport prints the post-boot verdict to the operator, then clears
// the report so the next --reboot-test starts a fresh cycle. Exit is 1 iff
// any previously-passing check regressed.
func showRebootReport(noColor, jsonOut, quiet bool) int {
	rep, err := readReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: cannot read reboot-test report: %v\n", err)
		return 1
	}
	if !quiet {
		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
		} else {
			printRebootReport(os.Stdout, rep, !noColor && isTerminal(os.Stdout))
		}
	}
	_ = os.Remove(rebootReportFile)
	_ = os.Remove(rebootStateFile)
	if rep.Regressions > 0 {
		return 1
	}
	return 0
}

// diffRebootResults matches pre and post grades by task ID then check ID
// (robust to ordering) and classifies each check's reboot fate.
func diffRebootResults(st rebootState, post []*check.TaskResult) rebootReport {
	rep := rebootReport{
		Version:    1,
		StartedAt:  st.StartedAt,
		FinishedAt: time.Now().UTC(),
	}
	postByTask := make(map[string]*check.TaskResult, len(post))
	for _, tr := range post {
		postByTask[tr.TaskID] = tr
	}
	for _, preTR := range st.Pre {
		td := rebootTaskDiff{TaskID: preTR.TaskID, Title: preTR.Title}
		postChecks := map[string]check.Result{}
		if postTR, ok := postByTask[preTR.TaskID]; ok {
			for _, r := range postTR.Checks {
				postChecks[r.CheckID] = r
			}
		}
		for _, preC := range preTR.Checks {
			postC, evaluated := postChecks[preC.CheckID]
			status := classifyReboot(preC.Passed, postC.Passed, evaluated)
			if status == statusRegressed {
				rep.Regressions++
			}
			td.Checks = append(td.Checks, rebootCheckDiff{
				CheckID:     preC.CheckID,
				Description: preC.Description,
				PrePassed:   preC.Passed,
				PostPassed:  evaluated && postC.Passed,
				PostDetail:  postC.Detail,
				Status:      status,
			})
		}
		rep.Tasks = append(rep.Tasks, td)
	}
	return rep
}

func classifyReboot(pre, post, evaluated bool) string {
	if !evaluated {
		return statusUnknown
	}
	switch {
	case pre && post:
		return statusPersisted
	case pre && !post:
		return statusRegressed
	case !pre && post:
		return statusRecovered
	default:
		return statusStillFailing
	}
}

// printRebootReport renders the diff in the same boxed style as render.Human.
func printRebootReport(w io.Writer, rep rebootReport, color bool) {
	const (
		green  = "\x1b[32m"
		red    = "\x1b[31m"
		yellow = "\x1b[33m"
		dim    = "\x1b[90m"
		blue   = "\x1b[34m"
	)
	fmt.Fprintln(w, "== lee-grade · reboot-persistence report ==")
	fmt.Fprintf(w, "   pre-reboot %s   post-reboot %s\n\n",
		rep.StartedAt.Format(time.RFC3339), rep.FinishedAt.Format(time.RFC3339))

	for _, td := range rep.Tasks {
		header := sanitize(fmt.Sprintf("Task %s · %s", td.TaskID, td.Title))
		fmt.Fprintln(w, col(color, blue, "┌─ "+header+" "+strings.Repeat("─", max(0, 70-3-utf8.RuneCountInString(header)))+"┐"))
		taskReg := 0
		for _, cd := range td.Checks {
			var glyph, label, code string
			switch cd.Status {
			case statusPersisted:
				glyph, label, code = "✓", "persisted", green
			case statusRegressed:
				glyph, label, code = "✗", "REGRESSED — was passing before reboot", red
				taskReg++
			case statusRecovered:
				glyph, label, code = "+", "recovered after reboot", yellow
			case statusStillFailing:
				glyph, label, code = "·", "still failing (also failed pre-reboot)", dim
			default:
				glyph, label, code = "?", "not evaluated after reboot", yellow
			}
			fmt.Fprintln(w, col(color, blue, "│ ")+col(color, code, glyph)+" "+sanitize(cd.Description)+"  "+col(color, dim, "["+label+"]"))
			if cd.Status == statusRegressed && cd.PostDetail != "" {
				fmt.Fprintln(w, col(color, blue, "│ ")+"    "+col(color, dim, "post-reboot: "+sanitize(cd.PostDetail)))
			}
		}
		fmt.Fprintln(w, col(color, blue, "│"))
		verdict, vcode := "verdict: all previously-passing checks survived the reboot", green
		if taskReg > 0 {
			verdict = fmt.Sprintf("verdict: %d regression(s) — configuration did NOT fully survive reboot", taskReg)
			vcode = red
		}
		fmt.Fprintln(w, col(color, blue, "│ ")+col(color, vcode, verdict))
		fmt.Fprintln(w, col(color, blue, "└"+strings.Repeat("─", 69)+"┘"))
		fmt.Fprintln(w)
	}

	if rep.Regressions > 0 {
		fmt.Fprintln(w, col(color, red, fmt.Sprintf("FAIL: %d regression(s) across %d task(s) — config is not reboot-safe.", rep.Regressions, len(rep.Tasks))))
	} else {
		fmt.Fprintln(w, col(color, green, fmt.Sprintf("PASS: every check that passed pre-reboot still passes — config is reboot-safe (%d task(s)).", len(rep.Tasks))))
	}
}

func col(color bool, code, s string) string {
	if !color {
		return s
	}
	return code + s + "\x1b[0m"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func readState() (rebootState, error) {
	var st rebootState
	b, err := os.ReadFile(rebootStateFile)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func readReport() (rebootReport, error) {
	var rep rebootReport
	b, err := os.ReadFile(rebootReportFile)
	if err != nil {
		return rep, err
	}
	return rep, json.Unmarshal(b, &rep)
}

// stageSelf copies this executable into a system bin dir and relabels it
// bin_t so the post-boot systemd unit (running as init_t) can exec it. When
// lee-grade is launched from somewhere init_t can't exec — most commonly a
// home directory under SELinux — pointing the unit at the original path fails
// with status=203/EXEC. Returns the staged path to use in ExecStart.
func stageSelf(self string) (string, error) {
	src, err := os.Open(self)
	if err != nil {
		return "", fmt.Errorf("open self: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(rebootStagedBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", rebootStagedBin, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return "", fmt.Errorf("copy to %s: %w", rebootStagedBin, err)
	}
	if err := dst.Close(); err != nil {
		return "", fmt.Errorf("finalize %s: %w", rebootStagedBin, err)
	}
	// Best-effort relabel; harmless (and expected to fail) when SELinux is off.
	if out, err := exec.Command("chcon", "-t", "bin_t", rebootStagedBin).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "lee-grade: note: could not relabel staged binary bin_t (%v: %s)\n", err, strings.TrimSpace(string(out)))
	}
	return rebootStagedBin, nil
}

// installRebootUnit writes and enables a one-shot unit that re-invokes the
// staged binary after the next boot. After= orders it past the services our
// checks inspect; missing units in After= are simply ignored by systemd.
func installRebootUnit(execPath string) error {
	unit := fmt.Sprintf(`[Unit]
Description=lee-grade reboot-persistence test (auto-generated, self-removing)
After=network-online.target firewalld.service crond.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s --reboot-test-resume

[Install]
WantedBy=multi-user.target
`, execPath)
	if err := os.WriteFile(rebootUnitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rebootUnitPath, err)
	}
	if out, err := exec.Command("systemctl", "enable", rebootUnitName).CombinedOutput(); err != nil {
		// Don't leave a half-armed unit file on disk if enable failed.
		_ = os.Remove(rebootUnitPath)
		return fmt.Errorf("systemctl enable: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeRebootUnit() {
	_ = exec.Command("systemctl", "disable", rebootUnitName).Run()
	_ = os.Remove(rebootUnitPath)
	_ = os.Remove(rebootStagedBin)
	_ = exec.Command("systemctl", "daemon-reload").Run()
}

func taskArgString(taskPath, tasksDir string) string {
	switch {
	case taskPath != "":
		return " --task " + taskPath
	case tasksDir != "":
		return " --tasks-dir " + tasksDir
	}
	return ""
}
