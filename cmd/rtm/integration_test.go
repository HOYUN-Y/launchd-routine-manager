package main

import (
	"bytes"
	"fmt"
	"html"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// Real launchd, real ~/Library/LaunchAgents. Only runs with RTM_INTEGRATION=1, and only ever
// touches dev.rtm.test.* labels: RTM_TEST_LABELS_ONLY=1 makes every state change on any other
// label fail inside the launchd package. Each job is booted out and its plist (and any backup
// of it) deleted at the end.
//
//	RTM_INTEGRATION=1 go test -count=1 -run TestIntegration -v ./cmd/rtm

// itest is the shared setup of the integration tests.
type itest struct {
	env    sys.Env
	agents string
	logs   string
	create func(t *testing.T, name, script, extra string) string
}

func (it *itest) read(name, ext string) string {
	b, _ := os.ReadFile(filepath.Join(it.logs, name+"."+ext))
	return string(b)
}

// newIntegration skips unless RTM_INTEGRATION=1, turns on the dev guard, and registers a final
// check that nothing of ours is left: no plist, no process, no backup file.
func newIntegration(t *testing.T) *itest {
	t.Helper()
	if os.Getenv("RTM_INTEGRATION") != "1" {
		t.Skip("set RTM_INTEGRATION=1 to run against the real launchd (dev.rtm.test.* jobs only)")
	}
	t.Setenv(launchd.GuardEnv, "1")
	env, err := sys.Real()
	if err != nil {
		t.Fatal(err)
	}
	it := &itest{env: env, agents: filepath.Join(env.Home, "Library", "LaunchAgents"), logs: t.TempDir()}

	backups := launchd.BackupDir(env.Home)
	rtmDir := filepath.Dir(backups)
	_, errB := os.Stat(backups)
	_, errR := os.Stat(rtmDir)
	madeBackupDir, madeRtmDir := errB != nil, errR != nil

	// Registered first, so it runs last: nothing may be left behind.
	t.Cleanup(func() {
		mine, _ := filepath.Glob(filepath.Join(backups, launchd.TestPrefix+"*"))
		for _, f := range mine {
			os.Remove(f) // backups of our own test labels
		}
		if madeBackupDir {
			os.Remove(backups) // only succeeds if empty
		}
		if madeRtmDir {
			os.Remove(rtmDir)
		}
		left, _ := filepath.Glob(filepath.Join(it.agents, launchd.TestPrefix+"*"))
		hidden, _ := filepath.Glob(filepath.Join(it.agents, ".dev.rtm.test.*")) // WritePlist temp files
		if len(left)+len(hidden) != 0 {
			t.Errorf("leftover test files in LaunchAgents: %v %v", left, hidden)
		}
		if out, _ := osexec.Command("pgrep", "-f", "sleep 311").Output(); len(out) != 0 {
			t.Errorf("leftover test process (sleep 311): pid %s", strings.TrimSpace(string(out)))
		}
		if left, _ := filepath.Glob(filepath.Join(backups, launchd.TestPrefix+"*")); len(left) != 0 {
			t.Errorf("leftover test backups: %v", left)
		}
	})

	// create writes dev.rtm.test.<name>.plist and arranges its removal.
	it.create = func(t *testing.T, name, script, extra string) string {
		t.Helper()
		label := launchd.TestPrefix + name
		path := filepath.Join(it.agents, label+".plist")
		launchd.Bootout(env.Runner, env.UID, label) // leftovers of an aborted earlier run; error = not loaded
		os.Remove(path)
		t.Cleanup(func() {
			launchd.Bootout(env.Runner, env.UID, label)
			os.Remove(path)
		})
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string><string>%s</string></array>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
%s</dict></plist>
`, label, html.EscapeString(script), filepath.Join(it.logs, name+".out"), filepath.Join(it.logs, name+".err"), extra)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := osexec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %s", out)
		}
		return label
	}
	return it
}

func TestIntegrationRun(t *testing.T) {
	it := newIntegration(t)
	env, create, read := it.env, it.create, it.read

	t.Run("ok exits 0", func(t *testing.T) {
		label := create(t, "ok", "echo hello-ok; exit 0", "")
		code, out, errw := exec(t, env, "run", label, "--load", "--wait", "15s")
		t.Logf("rtm run --load:\n%s%s", out, errw)
		if code != 0 || !strings.Contains(out, "finished: exit 0") || !strings.Contains(out, "registered with launchd first") {
			t.Fatalf("code=%d", code)
		}
		if st := launchd.Query(env.Runner, env.UID, label); !st.Loaded || st.LastExitCode == nil || *st.LastExitCode != 0 || st.Runs != 1 {
			t.Fatalf("status = %+v", st)
		}
		if n := strings.Count(read("ok", "out"), "hello-ok"); n != 1 {
			t.Fatalf("log has hello-ok %d times, want 1", n)
		}

		// already loaded: a plain run, the counter and the log move on
		code, out, errw = exec(t, env, "run", label, "--wait", "15s")
		t.Logf("rtm run (again):\n%s%s", out, errw)
		if code != 0 || !strings.Contains(out, "finished: exit 0 (runs 2)") || strings.Contains(out, "registered") {
			t.Fatalf("second run: code=%d", code)
		}
		if n := strings.Count(read("ok", "out"), "hello-ok"); n != 2 {
			t.Fatalf("log has hello-ok %d times, want 2", n)
		}
	})

	t.Run("fail exits 1", func(t *testing.T) {
		label := create(t, "fail", "echo boom >&2; exit 1", "")
		code, out, errw := exec(t, env, "run", label, "--load", "--wait", "15s")
		t.Logf("rtm run --load:\n%s%s", out, errw)
		if code != 1 || !strings.Contains(out, "finished: exit 1") || !strings.Contains(errw, "exited with 1") {
			t.Fatalf("code=%d", code)
		}
		if st := launchd.Query(env.Runner, env.UID, label); st.LastExitCode == nil || *st.LastExitCode != 1 {
			t.Fatalf("status = %+v", st)
		}
		if !strings.Contains(read("fail", "err"), "boom") {
			t.Fatalf("stderr log = %q", read("fail", "err"))
		}
		if _, out, _ := exec(t, env, "show", label); !strings.Contains(out, "last exit:  1") {
			t.Errorf("rtm show should report the exit code:\n%s", out)
		}
	})

	t.Run("not loaded needs --load", func(t *testing.T) {
		label := create(t, "unloaded", "exit 0", "")
		code, _, errw := exec(t, env, "run", label)
		if code != 1 || !strings.Contains(errw, "--load") {
			t.Fatalf("code=%d err=%q", code, errw)
		}
		if st := launchd.Query(env.Runner, env.UID, label); st.Loaded {
			t.Fatal("without --load the job must stay unloaded")
		}
	})

	t.Run("KeepAlive service: running refuses, restart needs --yes", func(t *testing.T) {
		label := create(t, "keep", "exec /bin/sleep 311", "<key>KeepAlive</key><true/>\n")
		code, out, errw := exec(t, env, "run", label, "--load", "--wait", "3s")
		t.Logf("rtm run --load:\n%s%s", out, errw)
		first := launchd.Query(env.Runner, env.UID, label)
		if code != 0 || first.State != "running" || first.PID == 0 {
			t.Fatalf("code=%d status=%+v", code, first)
		}

		code, _, errw = exec(t, env, "run", label)
		if code != 1 || !strings.Contains(errw, "already running") {
			t.Fatalf("plain run of a running job: code=%d err=%q", code, errw)
		}
		code, _, errw = exec(t, env, "run", label, "--restart")
		if code != 1 || !strings.Contains(errw, "--yes") {
			t.Fatalf("--restart without --yes: code=%d err=%q", code, errw)
		}
		if now := launchd.Query(env.Runner, env.UID, label); now.PID != first.PID {
			t.Fatalf("refused commands must not restart the job (pid %d -> %d)", first.PID, now.PID)
		}

		code, out, errw = exec(t, env, "run", label, "--restart", "--yes", "--wait", "3s")
		t.Logf("rtm run --restart --yes:\n%s%s", out, errw)
		after := launchd.Query(env.Runner, env.UID, label)
		if code != 0 || after.State != "running" || after.PID == 0 || after.PID == first.PID {
			t.Fatalf("code=%d pid %d -> %d state=%s", code, first.PID, after.PID, after.State)
		}
	})
}

// holds reads what launchd itself reports for label: the schedule it holds in memory.
func holds(t *testing.T, env sys.Env, label string) launchd.Status {
	t.Helper()
	st := launchd.Query(env.Runner, env.UID, label)
	if !st.Loaded {
		t.Fatalf("%s is not loaded", label)
	}
	return st
}

func TestIntegrationSchedule(t *testing.T) {
	it := newIntegration(t)
	env := it.env

	t.Run("change, reload, verify, revert", func(t *testing.T) {
		label := it.create(t, "sched", "echo sched; exit 0",
			"<key>StartInterval</key><integer>3600</integer>\n<key>EnvironmentVariables</key><dict><key>RTM_PROBE</key><string>keepme</string></dict>\n")
		path := filepath.Join(it.agents, label+".plist")
		original, _ := os.ReadFile(path)
		if err := launchd.Bootstrap(env.Runner, env.UID, label, path); err != nil {
			t.Fatal(err)
		}
		if st := holds(t, env, label); st.RunInterval != 3600 || len(st.Calendars) != 0 {
			t.Fatalf("start state: %+v", st)
		}

		// 1) interval -> daily
		code, out, errw := exec(t, env, "schedule", label, "--daily", "05:15")
		t.Logf("rtm schedule --daily 05:15:\n%s%s", out, errw)
		if code != 0 || !strings.Contains(out, "every 1h -> daily 05:15") || !strings.Contains(out, "reloaded: launchctl bootout + bootstrap") ||
			!strings.Contains(out, "verified: launchd now holds daily 05:15") {
			t.Fatalf("code=%d", code)
		}
		st := holds(t, env, label)
		if st.RunInterval != 0 || len(st.Calendars) != 1 || *st.Calendars[0].Hour != 5 || *st.Calendars[0].Minute != 15 {
			t.Fatalf("launchd holds %+v %+v", st, st.Calendars)
		}
		raw, _ := osexec.Command("launchctl", "print", fmt.Sprintf("gui/%d/%s", env.UID, label)).Output()
		if !strings.Contains(string(raw), `"Hour" => 5`) || strings.Contains(string(raw), "run interval") {
			t.Errorf("raw launchctl print does not show the change:\n%s", raw)
		}
		after, _ := os.ReadFile(path)
		if !bytes.HasPrefix(after, []byte("<?xml")) || !strings.Contains(string(after), "keepme") || strings.Contains(string(after), "StartInterval") {
			t.Errorf("plist after the change:\n%s", after)
		}
		if _, out, _ := exec(t, env, "show", label); !strings.Contains(out, "schedule:   daily 05:15") {
			t.Errorf("rtm show:\n%s", out)
		}
		backups, _ := filepath.Glob(filepath.Join(launchd.BackupDir(env.Home), label+"-*.plist"))
		if len(backups) != 1 {
			t.Fatalf("backups = %v", backups)
		}
		if b, _ := os.ReadFile(backups[0]); !bytes.Equal(b, original) {
			t.Error("the backup must be the original plist, byte for byte")
		}
		if fi, _ := os.Stat(backups[0]); fi.Mode().Perm() != 0o600 {
			t.Errorf("backup mode = %v", fi.Mode().Perm())
		}

		// 2) daily -> weekly (two calendar entries)
		code, out, errw = exec(t, env, "schedule", label, "--weekly", "mon,wed", "06:30")
		t.Logf("rtm schedule --weekly mon,wed 06:30:\n%s%s", out, errw)
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		st = holds(t, env, label)
		if len(st.Calendars) != 2 {
			t.Fatalf("launchd holds %d calendar entries, want 2", len(st.Calendars))
		}
		seen := map[int]bool{}
		for _, c := range st.Calendars {
			if *c.Hour != 6 || *c.Minute != 30 {
				t.Errorf("entry %+v", c)
			}
			seen[*c.Weekday] = true
		}
		if !seen[1] || !seen[3] {
			t.Errorf("weekdays = %v, want Monday and Wednesday", seen)
		}

		// 3) --revert goes back one step: the daily plist
		code, out, errw = exec(t, env, "schedule", label, "--revert")
		t.Logf("rtm schedule --revert:\n%s%s", out, errw)
		if code != 0 || !strings.Contains(out, "weekly mon 06:30, weekly wed 06:30 -> daily 05:15") || !strings.Contains(out, "pre-revert") {
			t.Fatalf("code=%d", code)
		}
		st = holds(t, env, label)
		if len(st.Calendars) != 1 || *st.Calendars[0].Hour != 5 || *st.Calendars[0].Minute != 15 || st.RunInterval != 0 {
			t.Fatalf("after revert launchd holds %+v %+v", st, st.Calendars)
		}

		// 4) interval, then the same again: nothing to do
		if code, out, _ := exec(t, env, "schedule", label, "--every", "30m"); code != 0 || !strings.Contains(out, "every 30m") {
			t.Fatalf("--every: code=%d\n%s", code, out)
		}
		if st := holds(t, env, label); st.RunInterval != 1800 || len(st.Calendars) != 0 {
			t.Fatalf("launchd holds %+v", st)
		}
		if code, out, _ := exec(t, env, "schedule", label, "--every", "30m"); code != 0 || !strings.Contains(out, "already has this schedule") {
			t.Fatalf("repeat: code=%d\n%s", code, out)
		}
		// the whole way back to the start: the plist's other keys never changed
		final, _ := os.ReadFile(path)
		if !strings.Contains(string(final), "keepme") || !strings.Contains(string(final), "/bin/sh") {
			t.Errorf("other keys lost:\n%s", final)
		}
	})

	t.Run("running job needs --yes", func(t *testing.T) {
		label := it.create(t, "schedrun", "exec /bin/sleep 311", "<key>KeepAlive</key><true/>\n")
		path := filepath.Join(it.agents, label+".plist")
		if code, out, errw := exec(t, env, "run", label, "--load", "--wait", "3s"); code != 0 {
			t.Fatalf("run --load: code=%d\n%s%s", code, out, errw)
		}
		first := holds(t, env, label)
		if first.PID == 0 {
			t.Fatalf("not running: %+v", first)
		}
		before, _ := os.ReadFile(path)

		code, _, errw := exec(t, env, "schedule", label, "--daily", "04:44")
		t.Logf("rtm schedule on a running job: %s", errw)
		if code != 1 || !strings.Contains(errw, "--yes") {
			t.Fatalf("code=%d err=%q", code, errw)
		}
		if now := holds(t, env, label); now.PID != first.PID {
			t.Fatalf("a refused change must not stop the job (pid %d -> %d)", first.PID, now.PID)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Fatal("a refused change must not touch the plist")
		}

		code, out, errw := exec(t, env, "schedule", label, "--daily", "04:44", "--yes")
		t.Logf("rtm schedule --yes:\n%s%s", out, errw)
		if code != 0 || !strings.Contains(out, "verified: launchd now holds daily 04:44") || !strings.Contains(out, "note: RunAtLoad/KeepAlive") {
			t.Fatalf("code=%d", code)
		}
		st := holds(t, env, label)
		if len(st.Calendars) != 1 || *st.Calendars[0].Hour != 4 || *st.Calendars[0].Minute != 44 {
			t.Fatalf("launchd holds %+v", st.Calendars)
		}
		if st.PID == first.PID {
			t.Errorf("the reload must have restarted the job (pid stayed %d)", st.PID)
		}
	})
}
