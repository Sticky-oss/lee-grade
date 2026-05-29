package check

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCronJob_userCrontabMatch(t *testing.T) {
	crontab := "# m h dom mon dow command\n" +
		"0 2 * * * /opt/backup.sh --full\n" +
		"@reboot /usr/local/bin/warmup\n"
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return crontab, nil
	})

	if r := checkCronJob(loadCheck(t, `{id: c, description: backup, type: cron-job, user: rocky, schedule: "0 2 * * *", command: /opt/backup.sh}`)); !r.Passed {
		t.Errorf("schedule+command match should pass, got %+v", r)
	}
	if r := checkCronJob(loadCheck(t, `{id: c, description: reboot, type: cron-job, user: rocky, schedule: "@reboot", command: warmup}`)); !r.Passed {
		t.Errorf("@reboot shorthand should pass, got %+v", r)
	}
	if r := checkCronJob(loadCheck(t, `{id: c, description: wrongtime, type: cron-job, user: rocky, schedule: "0 3 * * *", command: /opt/backup.sh}`)); r.Passed {
		t.Errorf("wrong schedule should fail, got %+v", r)
	}
}

func TestCronJob_noCrontabIsEmptyNotError(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "no crontab for bob\n", fmt.Errorf("exit status 1")
	})
	// Wanted present → fails cleanly (no Error).
	r := checkCronJob(loadCheck(t, `{id: c, description: x, type: cron-job, user: bob, command: /opt/backup.sh}`))
	if r.Error != "" {
		t.Errorf("missing crontab should be a clean fail, not Error: %+v", r)
	}
	if r.Passed {
		t.Errorf("no crontab should fail a present check, got %+v", r)
	}
	// present:false → passes (job genuinely absent).
	r2 := checkCronJob(loadCheck(t, `{id: c, description: x, type: cron-job, user: bob, command: /opt/backup.sh, present: false}`))
	if !r2.Passed {
		t.Errorf("absent job should satisfy present:false, got %+v", r2)
	}
}

func TestCronJob_explicitFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "backup")
	content := "# /etc/cron.d style — has a user field\n" +
		"30 1 * * * root /usr/sbin/logrotate /etc/logrotate.conf\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := checkCronJob(loadCheck(t, fmt.Sprintf(`{id: c, description: rot, type: cron-job, file: %q, schedule: "30 1 * * *", command: logrotate}`, f))); !r.Passed {
		t.Errorf("file-based match should pass, got %+v", r)
	}

	missing := filepath.Join(dir, "does-not-exist")
	if r := checkCronJob(loadCheck(t, fmt.Sprintf(`{id: c, description: absent, type: cron-job, file: %q, command: logrotate, present: false}`, missing))); !r.Passed {
		t.Errorf("missing file should satisfy present:false, got %+v", r)
	}
}

func TestCronJob_matchesRegex(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "*/5 * * * * /usr/bin/python3 /opt/poll.py\n", nil
	})
	if r := checkCronJob(loadCheck(t, `{id: c, description: re, type: cron-job, matches: 'python3 .*poll\.py'}`)); !r.Passed {
		t.Errorf("regex match should pass, got %+v", r)
	}
}

func TestCronJob_validationAndBadRegex(t *testing.T) {
	if r := checkCronJob(loadCheck(t, `{id: c, description: empty, type: cron-job, user: rocky}`)); r.Error == "" {
		t.Errorf("no predicate should error")
	}
	stubRunCmd(t, func(name string, args ...string) (string, error) { return "", nil })
	if r := checkCronJob(loadCheck(t, `{id: c, description: bad, type: cron-job, matches: '([unclosed'}`)); r.Error == "" {
		t.Errorf("invalid regex should error")
	}
}

func TestCronScheduleMatches(t *testing.T) {
	cases := []struct {
		line, want string
		expect     bool
	}{
		{"0 2 * * * /opt/backup.sh", "0 2 * * *", true},
		{"0  2   *  *  * /opt/backup.sh", "0 2 * * *", true},
		{"30 1 * * * root /usr/sbin/logrotate", "30 1 * * *", true},
		{"0 2 * * * /opt/backup.sh", "0 3 * * *", false},
		{"@reboot /usr/local/bin/warmup", "@reboot", true},
		{"@daily /x", "@reboot", false},
	}
	for _, c := range cases {
		if got := cronScheduleMatches(c.line, c.want); got != c.expect {
			t.Errorf("cronScheduleMatches(%q, %q) = %v, want %v", c.line, c.want, got, c.expect)
		}
	}
}
