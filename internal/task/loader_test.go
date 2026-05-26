package task

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempTask is a tiny helper for tests that need a YAML file on disk —
// keeps the test bodies focused on assertions, not setup boilerplate.
func writeTempTask(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "task.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp task: %v", err)
	}
	return path
}

func TestLoadFile_minimalTaskParses(t *testing.T) {
	path := writeTempTask(t, `
id: t1
title: Minimal task
domain: example
checks:
  - id: c1
    description: Some end state
    type: file
    path: /etc/passwd
`)
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.ID != "t1" || got.Title != "Minimal task" || got.Domain != "example" {
		t.Errorf("metadata wrong: %+v", got)
	}
	if len(got.Checks) != 1 {
		t.Fatalf("want 1 check, got %d", len(got.Checks))
	}
	if got.Checks[0].Type != "file" {
		t.Errorf("check type wrong: %q", got.Checks[0].Type)
	}
	if got.Checks[0].Args["path"] != "/etc/passwd" {
		t.Errorf("inline arg 'path' not captured: %v", got.Checks[0].Args)
	}
}

func TestLoadFile_rejectsMissingID(t *testing.T) {
	path := writeTempTask(t, `
title: No ID
checks:
  - id: c1
    description: x
    type: file
    path: /etc/passwd
`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected validation error for missing id, got nil")
	}
}

func TestLoadFile_rejectsDuplicateCheckIDs(t *testing.T) {
	path := writeTempTask(t, `
id: t1
title: Duplicate
checks:
  - id: dup
    description: A
    type: file
    path: /a
  - id: dup
    description: B
    type: file
    path: /b
`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected duplicate-check-id error, got nil")
	}
}

func TestLoadFile_rejectsFutureSchemaVersion(t *testing.T) {
	path := writeTempTask(t, `
schema_version: 99
id: t1
title: From the future
checks:
  - id: c1
    description: x
    type: file
    path: /etc/passwd
`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected schema_version error, got nil")
	}
}

func TestCheck_DecodeArgs_roundTripsTypedStruct(t *testing.T) {
	path := writeTempTask(t, `
id: t1
title: Decode test
checks:
  - id: c1
    description: x
    type: file
    path: /etc/passwd
    mode: 0o644
    owner: root
`)
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	var args struct {
		Path  string `yaml:"path"`
		Mode  *int   `yaml:"mode"`
		Owner string `yaml:"owner"`
	}
	if err := got.Checks[0].DecodeArgs(&args); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if args.Path != "/etc/passwd" {
		t.Errorf("path: got %q", args.Path)
	}
	if args.Mode == nil || *args.Mode != 0o644 {
		t.Errorf("mode: got %v", args.Mode)
	}
	if args.Owner != "root" {
		t.Errorf("owner: got %q", args.Owner)
	}
}
