package check

import (
	"fmt"
	"strconv"
	"strings"
)

// Remote inspection paths for the checks whose local implementation uses Go
// stdlib (os.Stat, os/user, /proc/mounts) rather than the runCmd seam. Each
// mirrors its local counterpart's assertions but gathers state from the node
// over SSH with a single coreutils command (stat / getent / id / cat), routed
// through runOn. Local behaviour (host == "") is untouched — these run only
// when a check carries host:.

// checkFileRemote verifies a file's existence / kind / mode / owner / group on
// a remote node via `stat`. stat without -L reports the link itself (lstat
// semantics), matching the local checker's os.Lstat.
func checkFileRemote(host string, args fileArgs) Result {
	out, err := runOn(host, "stat", "-c", "%a|%U|%G|%F", "--", args.Path)
	exists := err == nil

	if args.Exists != nil && !*args.Exists {
		if exists {
			return Result{Passed: false, Detail: fmt.Sprintf("expected %s to be absent on %s", args.Path, host)}
		}
		return Result{Passed: true}
	}
	if !exists {
		if strings.Contains(out, "No such file") {
			return Result{Passed: false, Detail: fmt.Sprintf("%s does not exist on %s", args.Path, host)}
		}
		return Result{Passed: false, Error: fmt.Sprintf("stat %s on %s: %v: %s", args.Path, host, err, strings.TrimSpace(out))}
	}

	fields := strings.SplitN(strings.TrimSpace(out), "|", 4)
	if len(fields) < 4 {
		return Result{Passed: false, Error: fmt.Sprintf("unexpected stat output for %s on %s: %q", args.Path, host, out)}
	}
	perm, owner, group, kindDesc := fields[0], fields[1], fields[2], fields[3]

	if args.Kind != "" {
		if k := statKind(kindDesc); k != args.Kind {
			return Result{Passed: false, Detail: fmt.Sprintf("%s is a %s on %s, want a %s", args.Path, k, host, args.Kind)}
		}
	}
	if args.Mode != nil {
		actual, perr := strconv.ParseInt(perm, 8, 0)
		if perr != nil {
			return Result{Passed: false, Error: fmt.Sprintf("parse mode %q for %s on %s: %v", perm, args.Path, host, perr)}
		}
		if int(actual) != *args.Mode {
			return Result{Passed: false, Detail: fmt.Sprintf("%s has mode %04o on %s, want %04o", args.Path, actual, host, *args.Mode)}
		}
	}
	if args.Owner != "" && owner != args.Owner {
		return Result{Passed: false, Detail: fmt.Sprintf("%s is owned by %s on %s, want %s", args.Path, owner, host, args.Owner)}
	}
	if args.Group != "" && group != args.Group {
		return Result{Passed: false, Detail: fmt.Sprintf("%s group is %s on %s, want %s", args.Path, group, host, args.Group)}
	}
	return Result{Passed: true}
}

// statKind maps GNU stat's %F description onto the checker's kind vocabulary.
func statKind(statF string) string {
	s := strings.ToLower(statF)
	switch {
	case strings.Contains(s, "directory"):
		return "directory"
	case strings.Contains(s, "symbolic link"):
		return "symlink"
	default:
		return "file"
	}
}

// checkUserRemote verifies a user via `getent passwd` (+ `getent group` for the
// primary group name), the network-database-aware equivalent of os/user.
func checkUserRemote(host string, args userArgs) Result {
	out, err := runOn(host, "getent", "passwd", args.Name)
	if err != nil || strings.TrimSpace(out) == "" {
		return Result{Passed: false, Detail: fmt.Sprintf("user %q does not exist on %s", args.Name, host)}
	}
	f := strings.Split(strings.TrimSpace(out), ":") // name:x:uid:gid:gecos:home:shell
	if len(f) < 7 {
		return Result{Passed: false, Error: fmt.Sprintf("unexpected passwd line for %s on %s: %q", args.Name, host, out)}
	}
	uid, _ := strconv.Atoi(f[2])
	gid, gecos, home, shell := f[3], f[4], f[5], f[6]

	if args.UID != nil && uid != *args.UID {
		return Result{Passed: false, Detail: fmt.Sprintf("user %s has UID %d on %s, want %d", args.Name, uid, host, *args.UID)}
	}
	if args.PrimaryGroup != "" {
		gout, gerr := runOn(host, "getent", "group", gid)
		name := ""
		if gerr == nil {
			if gf := strings.Split(strings.TrimSpace(gout), ":"); len(gf) > 0 {
				name = gf[0]
			}
		}
		if name != args.PrimaryGroup {
			return Result{Passed: false, Detail: fmt.Sprintf("user %s primary group is %s on %s, want %s", args.Name, name, host, args.PrimaryGroup)}
		}
	}
	if args.Shell != "" && shell != args.Shell {
		return Result{Passed: false, Detail: fmt.Sprintf("user %s shell is %s on %s, want %s", args.Name, shell, host, args.Shell)}
	}
	if args.Home != "" && home != args.Home {
		return Result{Passed: false, Detail: fmt.Sprintf("user %s home is %s on %s, want %s", args.Name, home, host, args.Home)}
	}
	if args.Comment != "" && gecos != args.Comment {
		return Result{Passed: false, Detail: fmt.Sprintf("user %s comment is %q on %s, want %q", args.Name, gecos, host, args.Comment)}
	}
	return Result{Passed: true}
}

// checkGroupRemote verifies a group via `getent group`.
func checkGroupRemote(host string, args groupArgs) Result {
	out, err := runOn(host, "getent", "group", args.Name)
	if err != nil || strings.TrimSpace(out) == "" {
		return Result{Passed: false, Detail: fmt.Sprintf("group %q does not exist on %s", args.Name, host)}
	}
	f := strings.Split(strings.TrimSpace(out), ":") // name:x:gid:members
	if len(f) < 3 {
		return Result{Passed: false, Error: fmt.Sprintf("unexpected group line for %s on %s: %q", args.Name, host, out)}
	}
	if args.GID != nil {
		gid, _ := strconv.Atoi(f[2])
		if gid != *args.GID {
			return Result{Passed: false, Detail: fmt.Sprintf("group %s has GID %d on %s, want %d", args.Name, gid, host, *args.GID)}
		}
	}
	return Result{Passed: true}
}

// checkUserInGroupRemote verifies membership via `id -nG`, which lists the
// user's primary + supplementary group names.
func checkUserInGroupRemote(host string, args userInGroupArgs) Result {
	out, err := runOn(host, "id", "-nG", args.User)
	if err != nil {
		return Result{Passed: false, Detail: fmt.Sprintf("user %q does not exist on %s", args.User, host)}
	}
	for _, g := range strings.Fields(out) {
		if g == args.Group {
			return Result{Passed: true}
		}
	}
	return Result{Passed: false, Detail: fmt.Sprintf("user %s is not a member of group %s on %s", args.User, args.Group, host)}
}
