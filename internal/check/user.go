package check

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("user", CheckerFunc(checkUser))
	Register("group", CheckerFunc(checkGroup))
	Register("user-in-group", CheckerFunc(checkUserInGroup))
}

// userArgs is the inline argument schema for `type: user`.
//
// Example YAML:
//
//	- id: alice-exists
//	  description: User alice exists with UID 2001
//	  type: user
//	  name: alice
//	  uid: 2001
//	  primary_group: sysadmins  # name OR gid (number) accepted
//	  shell: /bin/bash
//	  home: /home/alice
type userArgs struct {
	Name         string `yaml:"name"`
	UID          *int   `yaml:"uid,omitempty"`
	PrimaryGroup string `yaml:"primary_group,omitempty"` // group NAME (not GID)
	Shell        string `yaml:"shell,omitempty"`
	Home         string `yaml:"home,omitempty"`
	Comment      string `yaml:"comment,omitempty"`
}

func checkUser(c *task.Check) Result {
	var args userArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Name == "" {
		return Result{Error: "check 'user' requires field 'name'"}
	}
	if c.Host != "" {
		return checkUserRemote(c.Host, args)
	}

	u, err := user.Lookup(args.Name)
	if err != nil {
		if _, isUnknown := err.(user.UnknownUserError); isUnknown {
			return Result{Passed: false, Detail: fmt.Sprintf("user %q does not exist", args.Name)}
		}
		return Result{Passed: false, Error: fmt.Sprintf("lookup user %s: %v", args.Name, err)}
	}

	if args.UID != nil {
		actualUID, _ := strconv.Atoi(u.Uid)
		if actualUID != *args.UID {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"user %s has UID %d, want %d", args.Name, actualUID, *args.UID,
			)}
		}
	}
	if args.PrimaryGroup != "" {
		grp, err := user.LookupGroupId(u.Gid)
		if err != nil {
			return Result{Passed: false, Error: fmt.Sprintf("lookup gid %s: %v", u.Gid, err)}
		}
		if grp.Name != args.PrimaryGroup {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"user %s primary group is %s, want %s", args.Name, grp.Name, args.PrimaryGroup,
			)}
		}
	}
	if args.Shell != "" || args.Home != "" || args.Comment != "" {
		// /etc/passwd has shell + comment; os/user doesn't expose them.
		pw, err := parsePasswdEntry(args.Name)
		if err != nil {
			return Result{Passed: false, Error: err.Error()}
		}
		if args.Shell != "" && pw.shell != args.Shell {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"user %s shell is %s, want %s", args.Name, pw.shell, args.Shell,
			)}
		}
		if args.Home != "" && u.HomeDir != args.Home {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"user %s home is %s, want %s", args.Name, u.HomeDir, args.Home,
			)}
		}
		if args.Comment != "" && pw.gecos != args.Comment {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"user %s comment is %q, want %q", args.Name, pw.gecos, args.Comment,
			)}
		}
	}
	return Result{Passed: true}
}

// groupArgs is the inline argument schema for `type: group`.
type groupArgs struct {
	Name string `yaml:"name"`
	GID  *int   `yaml:"gid,omitempty"`
}

func checkGroup(c *task.Check) Result {
	var args groupArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Name == "" {
		return Result{Error: "check 'group' requires field 'name'"}
	}
	if c.Host != "" {
		return checkGroupRemote(c.Host, args)
	}
	g, err := user.LookupGroup(args.Name)
	if err != nil {
		if _, isUnknown := err.(user.UnknownGroupError); isUnknown {
			return Result{Passed: false, Detail: fmt.Sprintf("group %q does not exist", args.Name)}
		}
		return Result{Passed: false, Error: fmt.Sprintf("lookup group %s: %v", args.Name, err)}
	}
	if args.GID != nil {
		actualGID, _ := strconv.Atoi(g.Gid)
		if actualGID != *args.GID {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"group %s has GID %d, want %d", args.Name, actualGID, *args.GID,
			)}
		}
	}
	return Result{Passed: true}
}

// userInGroupArgs is the inline argument schema for `type: user-in-group`.
type userInGroupArgs struct {
	User  string `yaml:"user"`
	Group string `yaml:"group"`
}

func checkUserInGroup(c *task.Check) Result {
	var args userInGroupArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.User == "" || args.Group == "" {
		return Result{Error: "check 'user-in-group' requires fields 'user' and 'group'"}
	}
	if c.Host != "" {
		return checkUserInGroupRemote(c.Host, args)
	}
	u, err := user.Lookup(args.User)
	if err != nil {
		return Result{Passed: false, Detail: fmt.Sprintf("user %q does not exist", args.User)}
	}
	gids, err := u.GroupIds()
	if err != nil {
		return Result{Passed: false, Error: fmt.Sprintf("list groups for %s: %v", args.User, err)}
	}
	for _, gidStr := range gids {
		g, err := user.LookupGroupId(gidStr)
		if err != nil {
			continue
		}
		if g.Name == args.Group {
			return Result{Passed: true}
		}
	}
	return Result{Passed: false, Detail: fmt.Sprintf(
		"user %s is not a member of group %s", args.User, args.Group,
	)}
}

// ─── /etc/passwd helper ─────────────────────────────────────────────────────
// os/user exposes name/uid/gid/home but not shell or comment. We parse
// /etc/passwd directly for those — and ONLY for the specific name we want
// so a 50k-line passwd doesn't slow grading.

type passwdEntry struct {
	name  string
	uid   int
	gid   int
	gecos string
	home  string
	shell string
}

func parsePasswdEntry(name string) (*passwdEntry, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, fmt.Errorf("open /etc/passwd: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, name+":") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(fields[2])
		gid, _ := strconv.Atoi(fields[3])
		return &passwdEntry{
			name:  fields[0],
			uid:   uid,
			gid:   gid,
			gecos: fields[4],
			home:  fields[5],
			shell: fields[6],
		}, nil
	}
	return nil, fmt.Errorf("user %q not found in /etc/passwd", name)
}

// lookupUserByUID translates a numeric UID to a username via os/user.
// Used by the file checker for owner verification.
func lookupUserByUID(uid int) (string, error) {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// lookupGroupByGID translates a numeric GID to a group name.
func lookupGroupByGID(gid int) (string, error) {
	g, err := user.LookupGroupId(strconv.Itoa(gid))
	if err != nil {
		return "", err
	}
	return g.Name, nil
}
