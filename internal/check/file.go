package check

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("file", CheckerFunc(checkFile))
	Register("file-content", CheckerFunc(checkFileContent))
}

// fileArgs is the inline argument schema for `type: file`.
//
// Example YAML:
//
//	- id: fstab-mode
//	  description: /etc/fstab is mode 0644
//	  type: file
//	  path: /etc/fstab
//	  mode: 0644
//	  owner: root
//	  group: root
//	  kind: file
type fileArgs struct {
	Path  string `yaml:"path"`
	Mode  *int   `yaml:"mode,omitempty"`  // octal in YAML (`0644` not `644`)
	Owner string `yaml:"owner,omitempty"` // username string
	Group string `yaml:"group,omitempty"` // group name string
	Kind  string `yaml:"kind,omitempty"`  // "file" | "directory" | "symlink"
	// Exists defaults to true. Set explicitly to false to assert absence.
	Exists *bool `yaml:"exists,omitempty"`
}

func checkFile(c *task.Check) Result {
	var args fileArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Path == "" {
		return Result{Error: "check 'file' requires field 'path'"}
	}

	info, err := os.Lstat(args.Path)
	exists := err == nil

	// Absence assertion — passes iff the file is gone.
	if args.Exists != nil && !*args.Exists {
		if exists {
			return Result{Passed: false, Detail: fmt.Sprintf("expected %s to be absent", args.Path)}
		}
		return Result{Passed: true}
	}

	if !exists {
		if os.IsNotExist(err) {
			return Result{Passed: false, Detail: fmt.Sprintf("%s does not exist", args.Path)}
		}
		return Result{Passed: false, Error: fmt.Sprintf("stat %s: %v", args.Path, err)}
	}

	// Kind check.
	if args.Kind != "" {
		var actualKind string
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			actualKind = "symlink"
		case info.IsDir():
			actualKind = "directory"
		default:
			actualKind = "file"
		}
		if actualKind != args.Kind {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"%s is a %s, want a %s", args.Path, actualKind, args.Kind,
			)}
		}
	}

	// Mode check (compares only the permission bits — 07777).
	if args.Mode != nil {
		actualMode := int(info.Mode().Perm()) | int(info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
		// os.FileMode setuid/setgid/sticky bits are NOT in their POSIX
		// positions — translate to the chmod-style high bits.
		var posixSpecial int
		if info.Mode()&os.ModeSetuid != 0 {
			posixSpecial |= 04000
		}
		if info.Mode()&os.ModeSetgid != 0 {
			posixSpecial |= 02000
		}
		if info.Mode()&os.ModeSticky != 0 {
			posixSpecial |= 01000
		}
		actualMode = int(info.Mode().Perm()) | posixSpecial
		if actualMode != *args.Mode {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"%s has mode %04o, want %04o", args.Path, actualMode, *args.Mode,
			)}
		}
	}

	// Owner / group check via the Linux Stat_t.
	if args.Owner != "" || args.Group != "" {
		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return Result{Passed: false, Error: "filesystem does not expose Linux ownership"}
		}
		if args.Owner != "" {
			actualOwner, err := lookupUserByUID(int(sys.Uid))
			if err != nil {
				return Result{Passed: false, Error: fmt.Sprintf("lookup uid %d: %v", sys.Uid, err)}
			}
			if actualOwner != args.Owner {
				return Result{Passed: false, Detail: fmt.Sprintf(
					"%s is owned by %s, want %s", args.Path, actualOwner, args.Owner,
				)}
			}
		}
		if args.Group != "" {
			actualGroup, err := lookupGroupByGID(int(sys.Gid))
			if err != nil {
				return Result{Passed: false, Error: fmt.Sprintf("lookup gid %d: %v", sys.Gid, err)}
			}
			if actualGroup != args.Group {
				return Result{Passed: false, Detail: fmt.Sprintf(
					"%s group is %s, want %s", args.Path, actualGroup, args.Group,
				)}
			}
		}
	}

	return Result{Passed: true}
}

// fileContentArgs is the inline argument schema for `type: file-content`.
//
// Exactly ONE of equals / contains / matches must be set.
//
// Example YAML:
//
//	- id: fstab-has-sdb1
//	  description: /etc/fstab has a /dev/sdb1 entry
//	  type: file-content
//	  path: /etc/fstab
//	  matches: '^/dev/sdb1\s+\S+\s+xfs\s+'
type fileContentArgs struct {
	Path     string `yaml:"path"`
	Equals   string `yaml:"equals,omitempty"`
	Contains string `yaml:"contains,omitempty"`
	Matches  string `yaml:"matches,omitempty"` // regex (Go regexp.MustCompile syntax)
}

func checkFileContent(c *task.Check) Result {
	var args fileContentArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Path == "" {
		return Result{Error: "check 'file-content' requires field 'path'"}
	}
	set := 0
	if args.Equals != "" {
		set++
	}
	if args.Contains != "" {
		set++
	}
	if args.Matches != "" {
		set++
	}
	if set != 1 {
		return Result{Error: "check 'file-content' requires exactly one of: equals, contains, matches"}
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Passed: false, Detail: fmt.Sprintf("%s does not exist", args.Path)}
		}
		return Result{Passed: false, Error: fmt.Sprintf("read %s: %v", args.Path, err)}
	}
	content := string(data)

	switch {
	case args.Equals != "":
		if content == args.Equals {
			return Result{Passed: true}
		}
		return Result{Passed: false, Detail: fmt.Sprintf("%s content does not equal expected", args.Path)}
	case args.Contains != "":
		if strings.Contains(content, args.Contains) {
			return Result{Passed: true}
		}
		return Result{Passed: false, Detail: fmt.Sprintf("%s does not contain %q", args.Path, args.Contains)}
	case args.Matches != "":
		re, err := regexp.Compile(args.Matches)
		if err != nil {
			return Result{Error: fmt.Sprintf("invalid regex %q: %v", args.Matches, err)}
		}
		if re.MatchString(content) {
			return Result{Passed: true}
		}
		return Result{Passed: false, Detail: fmt.Sprintf("%s does not match /%s/", args.Path, args.Matches)}
	}
	return Result{Error: "unreachable"}
}
