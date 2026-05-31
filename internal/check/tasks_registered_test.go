package check

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sticky-oss/lee-grade/internal/task"
)

// Every check type referenced by a SHIPPED task file must have a registered
// implementation. A typo'd or not-yet-implemented `type:` otherwise passes all
// unit tests and only surfaces as an "unknown check type" error at grade time
// on a live host. This walks the real tasks/ tree (skipping solutions/, which
// hold Ansible playbooks, not lee-grade tasks).
func TestShippedTaskTypesAreRegistered(t *testing.T) {
	root := filepath.Join("..", "..", "tasks")
	registered := map[string]bool{}
	for _, ty := range RegisteredTypes() {
		registered[ty] = true
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/solutions/") {
			return nil // worked playbooks, not tasks
		}
		tk, lerr := task.LoadFile(path)
		if lerr != nil {
			t.Errorf("load %s: %v", path, lerr)
			return nil
		}
		count++
		for _, c := range tk.Checks {
			if !registered[c.Type] {
				t.Errorf("%s: check %q uses unregistered type %q", tk.ID, c.ID, c.Type)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("no .yaml task files found under %s", root)
	}
	t.Logf("validated %d shipped task files", count)
}
