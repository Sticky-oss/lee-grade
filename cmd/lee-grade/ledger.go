package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The progress ledger records, per task, how the learner has done over time:
// best and most-recent score, attempt count, and how many times they fully
// passed. `lab grade` folds each result in; `lee-grade --progress` (lab status)
// reads it back. It's a coaching aid, not authoritative state, so every write
// is best-effort — a rootless run that can't write the state dir just skips it
// rather than failing the grade.

// ledgerDir is where per-task records live. Overridable via LEE_GRADE_STATE
// (used by tests and rootless runs); defaults to the system state directory.
func ledgerDir() string {
	if d := os.Getenv("LEE_GRADE_STATE"); d != "" {
		return filepath.Join(d, "progress")
	}
	return "/var/lib/lee-grade/progress"
}

// ledgerEntry is the persisted progress for a single task.
type ledgerEntry struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	BestPct   int    `json:"best_pct"`
	LastPct   int    `json:"last_pct"`
	LastPass  int    `json:"last_pass"`
	LastTotal int    `json:"last_total"`
	Attempts  int    `json:"attempts"`
	Passes    int    `json:"passes"` // times the task was fully passed
	UpdatedAt string `json:"updated_at"`
}

// recordResult folds one grade into the task's ledger entry. Best-effort: any
// failure (unwritable state dir, malformed prior file) is swallowed so it can
// never break grading.
func recordResult(taskID, title string, passed, total, pct int) {
	if taskID == "" {
		return
	}
	dir := ledgerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, taskID+".json")

	var e ledgerEntry
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &e) // a corrupt prior file just resets the counters
	}
	e.TaskID = taskID
	e.Title = title
	e.Attempts++
	if pct > e.BestPct {
		e.BestPct = pct
	}
	e.LastPct = pct
	e.LastPass = passed
	e.LastTotal = total
	if total > 0 && passed == total {
		e.Passes++
	}
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	b, err := json.MarshalIndent(&e, "", "  ")
	if err != nil {
		return
	}
	// Write-then-rename so a crashed write never leaves a half-file the next
	// read would discard.
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

// loadLedger reads every entry, most-recently-touched first.
func loadLedger() []ledgerEntry {
	matches, _ := filepath.Glob(filepath.Join(ledgerDir(), "*.json"))
	var out []ledgerEntry
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var e ledgerEntry
		if json.Unmarshal(b, &e) == nil && e.TaskID != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// printProgress renders the ledger as a compact table.
func printProgress(w io.Writer, color bool) {
	const (
		green  = "\x1b[32m"
		yellow = "\x1b[33m"
		red    = "\x1b[31m"
		dim    = "\x1b[90m"
		blue   = "\x1b[34m"
	)
	entries := loadLedger()
	fmt.Fprintln(w, col(color, blue, "== lee-grade · progress ledger =="))
	if len(entries) == 0 {
		fmt.Fprintln(w, col(color, dim, "  no tasks graded yet — run `lab grade <id>` (or `lab list` to pick one)"))
		return
	}
	fmt.Fprintf(w, "  %-26s %6s %6s %7s %9s  %s\n",
		"TASK", "BEST", "LAST", "PASSES", "ATTEMPTS", "LAST GRADED")
	for _, e := range entries {
		// Colour the best score: green once fully passed, yellow partway, red
		// if never above zero.
		bestCode := red
		switch {
		case e.BestPct >= 100:
			bestCode = green
		case e.BestPct > 0:
			bestCode = yellow
		}
		date := e.UpdatedAt
		if t, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil {
			date = t.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "  %-26s %s %6s %7d %9d  %s\n",
			truncate(sanitize(e.TaskID), 26),
			col(color, bestCode, fmt.Sprintf("%5d%%", e.BestPct)),
			fmt.Sprintf("%d%%", e.LastPct),
			e.Passes, e.Attempts, col(color, dim, date))
	}
}
