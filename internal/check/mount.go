package check

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("mount", CheckerFunc(checkMount))
}

// mountArgs is the inline argument schema for `type: mount`.
//
// Example YAML:
//
//	- id: data-mounted
//	  description: /data is mounted as xfs
//	  type: mount
//	  mountpoint: /data
//	  fstype: xfs
//	  device: /dev/sdb1            # optional
//	  contains_options: [rw, noatime]  # optional, ALL must be present
type mountArgs struct {
	Mountpoint      string   `yaml:"mountpoint"`
	FsType          string   `yaml:"fstype,omitempty"`
	Device          string   `yaml:"device,omitempty"`
	ContainsOptions []string `yaml:"contains_options,omitempty"`
}

// mountEntry is one /proc/mounts line.
type mountEntry struct {
	device     string
	mountpoint string
	fstype     string
	options    string
}

func checkMount(c *task.Check) Result {
	var args mountArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Mountpoint == "" {
		return Result{Error: "check 'mount' requires field 'mountpoint'"}
	}

	mounts, err := readProcMounts(c.Host)
	if err != nil {
		return Result{Passed: false, Error: err.Error()}
	}

	var entry *mountEntry
	for i := range mounts {
		if mounts[i].mountpoint == args.Mountpoint {
			entry = &mounts[i]
			break
		}
	}
	if entry == nil {
		return Result{Passed: false, Detail: fmt.Sprintf("nothing mounted at %s", args.Mountpoint)}
	}
	if args.FsType != "" && entry.fstype != args.FsType {
		return Result{Passed: false, Detail: fmt.Sprintf(
			"%s is fstype %s, want %s", args.Mountpoint, entry.fstype, args.FsType,
		)}
	}
	if args.Device != "" && entry.device != args.Device {
		return Result{Passed: false, Detail: fmt.Sprintf(
			"%s is mounted from %s, want %s", args.Mountpoint, entry.device, args.Device,
		)}
	}
	for _, want := range args.ContainsOptions {
		if !optionsContain(entry.options, want) {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"%s options %q do not include %q", args.Mountpoint, entry.options, want,
			)}
		}
	}
	return Result{Passed: true}
}

func readProcMounts(host string) ([]mountEntry, error) {
	if host == "" {
		f, err := os.Open("/proc/mounts")
		if err != nil {
			return nil, fmt.Errorf("open /proc/mounts: %w", err)
		}
		defer f.Close()
		return parseProcMounts(f)
	}
	out, err := runOn(host, "cat", "/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("read /proc/mounts on %s: %v: %s", host, err, strings.TrimSpace(out))
	}
	return parseProcMounts(strings.NewReader(out))
}

func parseProcMounts(r io.Reader) ([]mountEntry, error) {
	var out []mountEntry
	s := bufio.NewScanner(r)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 4 {
			continue
		}
		out = append(out, mountEntry{
			device:     fields[0],
			mountpoint: fields[1],
			fstype:     fields[2],
			options:    fields[3],
		})
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("read /proc/mounts: %w", err)
	}
	return out, nil
}

func optionsContain(optsCsv, want string) bool {
	for _, o := range strings.Split(optsCsv, ",") {
		if o == want {
			return true
		}
	}
	return false
}
