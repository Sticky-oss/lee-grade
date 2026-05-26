# lee-grade

**A declarative RHCSA / RHCE task grader for real Linux boxes.**

`lee-grade` reads a task definition in YAML and verifies whether the host
it's running on satisfies each grading check. Same task DSL as
[lee-lab](https://github.com/Sticky-oss/lee-lab) (the browser-based
RHCSA simulator) — so a task you practiced in the sim can be graded
identically on a real Rocky 9 box.

```
$ lee-grade --task tasks/rhcsa-9/task1-users.yaml
┌─ Task task1-users · Create users alice and bob (user-group-management) ┐
│ ✓ Group "sysadmins" exists with GID 5000
│ ✓ User "alice" exists with UID 2001
│ ✓ alice's primary group is sysadmins
│ ✓ alice is in the wheel group
│ ✗ bob's password is locked
│     hint: usermod -L bob
│
│ 4 / 5 checks passed (80%)
└─────────────────────────────────────────────────────────────────────┘
```

## Why

The lee-lab browser sim grades simulated work. lee-grade grades real
work — letting boot-camp instructors verify student progress on actual
Linux VMs, or letting a self-studier check their own practice without
hand-running every shell command. Same rubric, real OS.

The killer feature (coming soon): `--reboot-test` actually reboots the
host and re-grades after boot, verifying the EX200's most-tested
rubric ("configurations must persist after reboot") on real hardware.

## Install

Pre-built binaries: see [Releases](https://github.com/Sticky-oss/lee-grade/releases).

From source:

```bash
go install github.com/sticky-oss/lee-grade/cmd/lee-grade@latest
```

Or clone + build:

```bash
git clone https://github.com/Sticky-oss/lee-grade
cd lee-grade
go build -o bin/lee-grade ./cmd/lee-grade
```

Requires Go 1.21+ to build. The resulting binary is fully static; no
runtime dependencies beyond standard Linux utilities (`systemctl`,
`rpm`, etc.) for the checks that shell out.

## Usage

```bash
# Grade one task
lee-grade --task tasks/rhcsa-9/task1-users.yaml

# Grade an entire directory of tasks
lee-grade --tasks-dir tasks/rhcsa-9

# Machine-readable output
lee-grade --task task1.yaml --json

# Exit code only — for CI
lee-grade --task task1.yaml --quiet && echo PASS

# List the alphabet of registered check types
lee-grade --list-check-types
```

Exit codes follow standard Unix conventions: `0` if every task fully
passed, `1` if any check failed, `2` for argument or task-file errors.

## Task DSL

A task is a YAML document with metadata and a list of declarative checks.
Each check has a `type` that selects a registered implementation:

```yaml
schema_version: 1
id: task1-users
title: Create user alice with UID 2001
domain: user-group-management
track: rhcsa-9

description: |
  Create a group "sysadmins" with GID 5000, then a user "alice" with
  UID 2001, primary group sysadmins, shell /bin/bash, comment "Alice".

checks:
  - id: group-exists
    description: Group "sysadmins" exists with GID 5000
    hint: groupadd -g 5000 sysadmins
    type: group
    name: sysadmins
    gid: 5000

  - id: alice-exists
    description: User alice exists with UID 2001
    hint: useradd -u 2001 -g sysadmins alice
    type: user
    name: alice
    uid: 2001
    primary_group: sysadmins
    shell: /bin/bash
```

See `tasks/rhcsa-9/demo-host-sanity.yaml` for a runnable example, and
`docs/check-types.md` (TODO) for the full check-type alphabet.

## Check types (v1)

Implemented:

| Type | Verifies |
|---|---|
| `file` | path exists, mode, owner, group, kind (file/dir/symlink) |
| `file-content` | file content equals / contains / matches regex |
| `user` | user exists, UID, primary group, shell, home, comment |
| `group` | group exists, GID |
| `user-in-group` | user is a member of group |
| `service-state` | systemd unit is active / enabled / masked |
| `package-installed` | rpm (or dpkg) reports installed; version match |
| `mount` | something is mounted at path with right fstype / device / options |

Planned for v2:

- `selinux-mode`, `selinux-boolean`, `selinux-context`, `selinux-port`
- `firewall-service`, `firewall-port`, `firewall-rich-rule`
- `cron-job`, `command-output`, `exit-code`
- `mount-persists` (verifies fstab entry that would re-mount on boot)

To add a new check type: implement `internal/check.Checker` and
`Register("my-type", &impl{})` in your file's `init()`.

## Design

- **Single static binary.** No daemons, no databases, no config files
  beyond the task YAMLs themselves.
- **Stateless.** Each invocation reads the host's current state and
  grades from scratch.
- **Declarative DSL.** Tasks describe what END STATE counts as "passed",
  not which commands to run. Same definitions feed lee-lab.
- **Read-mostly.** All v1 checks are read-only against the host. Future
  features like `--reboot-test` will mutate (with explicit consent).
- **Cross-distro tolerant.** Built primarily for RHEL/Rocky/CentOS/Fedora,
  but the `package-installed` check falls back to `dpkg` for Debian-like
  systems so the binary is useful in mixed labs.

## Status

**v0.1** — proof of concept (this commit). 8 check types, working
human/JSON output, lee-lab-compatible DSL.

Roadmap:
- v0.2 — SELinux + firewall checks
- v0.3 — `--reboot-test` (snapshot + reboot + resume)
- v0.4 — Signed task bundles
- v0.5 — RPM + Homebrew distribution
- v1.0 — Full RHCSA EX200 + RHCE EX294 task library

## License

[TBD] — see LICENSE when present.
