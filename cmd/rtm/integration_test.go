package main

import (
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
// label fail inside the launchd package. Each job is booted out and its plist deleted at the end.
//
//	RTM_INTEGRATION=1 go test -count=1 -run TestIntegration -v ./cmd/rtm
func TestIntegrationRun(t *testing.T) {
	if os.Getenv("RTM_INTEGRATION") != "1" {
		t.Skip("set RTM_INTEGRATION=1 to run against the real launchd (dev.rtm.test.* jobs only)")
	}
	t.Setenv(launchd.GuardEnv, "1")
	env, err := sys.Real()
	if err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(env.Home, "Library", "LaunchAgents")
	logs := t.TempDir()

	// Registered first, so it runs last: nothing may be left behind.
	t.Cleanup(func() {
		left, _ := filepath.Glob(filepath.Join(agents, launchd.TestPrefix+"*.plist"))
		if len(left) != 0 {
			t.Errorf("leftover test plists: %v", left)
		}
		if out, _ := osexec.Command("pgrep", "-f", "sleep 311").Output(); len(out) != 0 {
			t.Errorf("leftover test process (sleep 311): pid %s", strings.TrimSpace(string(out)))
		}
	})

	// create writes dev.rtm.test.<name>.plist and arranges its removal.
	create := func(t *testing.T, name, script, extra string) string {
		t.Helper()
		label := launchd.TestPrefix + name
		path := filepath.Join(agents, label+".plist")
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
`, label, html.EscapeString(script), filepath.Join(logs, name+".out"), filepath.Join(logs, name+".err"), extra)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := osexec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %s", out)
		}
		return label
	}
	read := func(name, ext string) string {
		b, _ := os.ReadFile(filepath.Join(logs, name+"."+ext))
		return string(b)
	}

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
