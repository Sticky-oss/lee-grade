package check

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("selinux", CheckerFunc(checkSELinux))
}

// selinuxArgs is the inline argument schema for `type: selinux`.
//
// One selinux check verifies exactly ONE aspect. The aspect is selected by
// which field is set:
//
//   - mode    → enforcement mode via `getenforce`
//   - boolean → SELinux boolean state via `getsebool`
//   - path    → file/dir SELinux context via `stat -c %C`
//   - port    → port label via `semanage port -l`
//
// Setting zero or more-than-one of those is an authoring error.
//
// Note: the SELinux *type* field cannot be named `type` here — the DSL
// reserves `type` for the check-type discriminator (see task.Check), so it
// never reaches Args. Use `setype` instead.
//
// Example YAML:
//
//	- id: httpd-content-context
//	  description: /srv/web is labeled httpd_sys_content_t
//	  type: selinux
//	  path: /srv/web
//	  setype: httpd_sys_content_t
type selinuxArgs struct {
	// Mode asserts `getenforce` equals enforcing | permissive | disabled
	// (case-insensitive).
	Mode string `yaml:"mode,omitempty"`

	// Boolean is an SELinux boolean name (e.g. httpd_can_network_connect).
	// Pair with State.
	Boolean string `yaml:"boolean,omitempty"`
	// State is the desired boolean state. Accepts on/off, true/false,
	// yes/no, 1/0 (quote it in YAML — bare `on`/`off` are YAML booleans).
	State string `yaml:"state,omitempty"`

	// Path is a filesystem path whose SELinux context is checked. Pair with
	// SEType (match just the type field) and/or Context (full label).
	Path string `yaml:"path,omitempty"`

	// Port is a port number whose label is checked via semanage. Pair with
	// Proto and SEType.
	Port  int    `yaml:"port,omitempty"`
	Proto string `yaml:"proto,omitempty"`

	// Context is a full SELinux context string for an exact match on a path
	// (e.g. system_u:object_r:httpd_sys_content_t:s0).
	Context string `yaml:"context,omitempty"`
	// SEType is the SELinux type component — the common RHCSA assertion for
	// both file contexts ("set the type to httpd_sys_content_t") and port
	// labels. For paths it matches the 3rd colon-field of the context.
	SEType string `yaml:"setype,omitempty"`
}

func checkSELinux(c *task.Check) Result {
	var args selinuxArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}

	// Exactly one aspect per check keeps result lines unambiguous.
	set := 0
	for _, on := range []bool{args.Mode != "", args.Boolean != "", args.Path != "", args.Port != 0} {
		if on {
			set++
		}
	}
	if set == 0 {
		return Result{Error: "check 'selinux' requires one of: mode, boolean, path, port"}
	}
	if set > 1 {
		return Result{Error: "check 'selinux' takes exactly one of: mode, boolean, path, port (split into separate checks)"}
	}

	switch {
	case args.Mode != "":
		return selinuxMode(args)
	case args.Boolean != "":
		return selinuxBoolean(args)
	case args.Path != "":
		return selinuxContext(args)
	default:
		return selinuxPort(args)
	}
}

func selinuxMode(args selinuxArgs) Result {
	out, err := runCmd("getenforce")
	if err != nil {
		return Result{Error: fmt.Sprintf("getenforce: %v (%s)", err, strings.TrimSpace(out))}
	}
	got := strings.ToLower(strings.TrimSpace(out))
	want := strings.ToLower(strings.TrimSpace(args.Mode))
	if got != want {
		return Result{Passed: false, Detail: fmt.Sprintf("SELinux mode is %q, want %q", got, want)}
	}
	return Result{Passed: true}
}

func selinuxBoolean(args selinuxArgs) Result {
	want, ok := normalizeOnOff(args.State)
	if !ok {
		return Result{Error: fmt.Sprintf("check 'selinux' boolean %q: invalid state %q (use on/off)", args.Boolean, args.State)}
	}
	out, err := runCmd("getsebool", args.Boolean)
	if err != nil {
		return Result{Error: fmt.Sprintf("getsebool %s: %v (%s)", args.Boolean, err, strings.TrimSpace(out))}
	}
	got, parsed := parseGetsebool(out)
	if !parsed {
		return Result{Error: fmt.Sprintf("getsebool %s: could not parse %q", args.Boolean, strings.TrimSpace(out))}
	}
	if got != want {
		return Result{Passed: false, Detail: fmt.Sprintf("boolean %s is %q, want %q", args.Boolean, got, want)}
	}
	return Result{Passed: true}
}

func selinuxContext(args selinuxArgs) Result {
	if args.SEType == "" && args.Context == "" {
		return Result{Error: fmt.Sprintf("check 'selinux' path %q requires setype and/or context", args.Path)}
	}
	out, err := runCmd("stat", "-c", "%C", args.Path)
	if err != nil {
		return Result{Passed: false, Detail: fmt.Sprintf("stat %s: %s", args.Path, strings.TrimSpace(out))}
	}
	ctx := strings.TrimSpace(out)
	if ctx == "" || ctx == "?" {
		return Result{Passed: false, Detail: fmt.Sprintf("%s has no SELinux context (got %q — is SELinux enabled?)", args.Path, ctx)}
	}
	if args.Context != "" && ctx != args.Context {
		return Result{Passed: false, Detail: fmt.Sprintf("%s context is %q, want %q", args.Path, ctx, args.Context)}
	}
	if args.SEType != "" {
		gotType := contextType(ctx)
		if gotType != args.SEType {
			return Result{Passed: false, Detail: fmt.Sprintf("%s type is %q, want %q (full context %q)", args.Path, gotType, args.SEType, ctx)}
		}
	}
	return Result{Passed: true}
}

func selinuxPort(args selinuxArgs) Result {
	if args.SEType == "" {
		return Result{Error: fmt.Sprintf("check 'selinux' port %d requires setype", args.Port)}
	}
	proto := strings.ToLower(args.Proto)
	if proto == "" {
		proto = "tcp"
	}
	out, err := runCmd("semanage", "port", "-l")
	if err != nil {
		return Result{Error: fmt.Sprintf("semanage port -l: %v (%s) — needs policycoreutils-python-utils", err, strings.TrimSpace(out))}
	}
	if semanagePortLabeled(out, args.SEType, proto, args.Port) {
		return Result{Passed: true}
	}
	return Result{Passed: false, Detail: fmt.Sprintf("port %d/%s is not labeled %s", args.Port, proto, args.SEType)}
}

// parseGetsebool extracts the on/off state from `getsebool <name>` output,
// which looks like: "httpd_can_network_connect --> on".
func parseGetsebool(out string) (state string, ok bool) {
	idx := strings.Index(out, "-->")
	if idx < 0 {
		return "", false
	}
	state = strings.TrimSpace(out[idx+len("-->"):])
	state = strings.ToLower(strings.Fields(state+" ")[0])
	if state == "on" || state == "off" {
		return state, true
	}
	return "", false
}

// contextType returns the SELinux type — the 3rd colon-separated field of a
// context like "unconfined_u:object_r:httpd_sys_content_t:s0". Returns the
// whole string if it doesn't have the expected shape.
func contextType(ctx string) string {
	parts := strings.Split(ctx, ":")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ctx
}

// semanagePortLabeled reports whether `semanage port -l` output assigns the
// given type to port/proto. Lines look like:
//
//	http_port_t                    tcp      80, 81, 443, 488, 8008, 8009, 8443, 9000
//	ssh_port_t                     tcp      22
func semanagePortLabeled(out, seType, proto string, port int) bool {
	portStr := strconv.Itoa(port)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != seType {
			continue
		}
		if !strings.EqualFold(fields[1], proto) {
			continue
		}
		// Remaining fields are comma-separated ports/ranges. Strip commas
		// and compare each token; tokens may be ranges like "6000-6020".
		for _, tok := range fields[2:] {
			tok = strings.TrimSuffix(tok, ",")
			if tok == portStr {
				return true
			}
			if lo, hi, ok := parsePortRange(tok); ok && port >= lo && port <= hi {
				return true
			}
		}
	}
	return false
}

func parsePortRange(tok string) (lo, hi int, ok bool) {
	dash := strings.Index(tok, "-")
	if dash < 0 {
		return 0, 0, false
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(tok[:dash]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(tok[dash+1:]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

// normalizeOnOff maps the assorted truthy/falsy spellings a YAML author
// might write to canonical "on"/"off".
func normalizeOnOff(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "true", "yes", "1", "enabled":
		return "on", true
	case "off", "false", "no", "0", "disabled":
		return "off", true
	default:
		return "", false
	}
}
