package task

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadFile parses a single YAML task file from disk and validates it.
// Returns the Task on success or a wrapped error that includes the file
// path so the CLI can surface "which file" without the caller plumbing
// that context.
func LoadFile(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var t Task
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

// LoadDir walks `dir` recursively, loading every *.yaml / *.yml file as
// a Task. Files that fail to parse or validate are returned in `errors`
// alongside the successfully-loaded tasks — the caller decides whether
// to abort or to grade what it can.
func LoadDir(dir string) (tasks []*Task, errors []error) {
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		t, loadErr := LoadFile(path)
		if loadErr != nil {
			errors = append(errors, loadErr)
			return nil
		}
		tasks = append(tasks, t)
		return nil
	})
	if walkErr != nil {
		errors = append(errors, fmt.Errorf("walk %s: %w", dir, walkErr))
	}
	return tasks, errors
}
