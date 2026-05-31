package main

import "testing"

func TestLedger_recordAndLoad(t *testing.T) {
	t.Setenv("LEE_GRADE_STATE", t.TempDir())

	recordResult("rhcsa-demo", "Demo task", 1, 3, 33)  // partial
	recordResult("rhcsa-demo", "Demo task", 3, 3, 100) // full pass
	recordResult("rhcsa-demo", "Demo task", 2, 3, 66)  // regress

	entries := loadLedger()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", e.Attempts)
	}
	if e.BestPct != 100 { // best is a high-water mark, not the latest
		t.Errorf("best = %d, want 100", e.BestPct)
	}
	if e.LastPct != 66 {
		t.Errorf("last = %d, want 66", e.LastPct)
	}
	if e.Passes != 1 { // only the 3/3 attempt counts as a pass
		t.Errorf("passes = %d, want 1", e.Passes)
	}
}

func TestLedger_emptyTaskIDIgnored(t *testing.T) {
	t.Setenv("LEE_GRADE_STATE", t.TempDir())
	recordResult("", "x", 1, 1, 100)
	if got := loadLedger(); len(got) != 0 {
		t.Errorf("empty task id should record nothing, got %d entries", len(got))
	}
}
