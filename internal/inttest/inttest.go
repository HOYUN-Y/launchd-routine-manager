// Package inttest is the shared setup of the integration tests that touch the real launchd.
// Tests only. Every test using it runs only with RTM_INTEGRATION=1.
//
// Safety, in layers: the dev guard (RTM_TEST_LABELS_ONLY=1) makes every state change on a label
// other than dev.rtm.test.* fail inside the launchd package; every job is booted out and its
// plist (and backups) deleted at the end; and a final check fails the test if anything is left,
// including a job that launchctl still lists.
package inttest

import (
	"fmt"
	"html"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

type Env struct {
	Env    sys.Env
	Agents string // ~/Library/LaunchAgents
	Logs   string // temp dir for the jobs' stdout/stderr
	labels []string
	sleepN int
}

// Sleep returns a shell command that sleeps "forever" under a number unique to this test
// process, so the leftover-process check cannot mistake another package's job for ours.
func (e *Env) Sleep() string {
	if e.sleepN == 0 {
		e.sleepN = 100000 + os.Getpid()%100000
	}
	return fmt.Sprintf("exec /bin/sleep %d", e.sleepN)
}

// Read returns a job's log file (name is the part after dev.rtm.test., ext is out or err).
func (e *Env) Read(name, ext string) string {
	b, _ := os.ReadFile(filepath.Join(e.Logs, name+"."+ext))
	return string(b)
}

func (e *Env) loaded(label string) bool {
	return launchd.Query(e.Env.Runner, e.Env.UID, label).Loaded
}

// New skips unless RTM_INTEGRATION=1, turns on the dev guard, and registers the final check.
func New(t *testing.T) *Env {
	t.Helper()
	if os.Getenv("RTM_INTEGRATION") != "1" {
		t.Skip("set RTM_INTEGRATION=1 to run against the real launchd (dev.rtm.test.* jobs only)")
	}
	t.Setenv(launchd.GuardEnv, "1")
	env, err := sys.Real()
	if err != nil {
		t.Fatal(err)
	}
	it := &Env{Env: env, Agents: filepath.Join(env.Home, "Library", "LaunchAgents"), Logs: t.TempDir()}

	backups := launchd.BackupDir(env.Home)
	rtmDir := filepath.Dir(backups)

	// Registered first, so it runs last: nothing may be left behind. Everything here is scoped to
	// the labels THIS test created: go test runs packages in parallel, and another package's
	// integration test owns its own dev.rtm.test.* files at the same time.
	t.Cleanup(func() {
		for _, label := range it.labels {
			mine, _ := filepath.Glob(filepath.Join(backups, label+"-*"))
			for _, f := range mine {
				os.Remove(f) // backups of our own test labels
			}
		}
		// Empty directories hold nothing of anyone's: remove them (Remove fails if not empty, so
		// another test's backups are safe). rtm recreates them on demand.
		os.Remove(backups)
		os.Remove(rtmDir)

		for _, label := range it.labels {
			if it.loaded(label) {
				t.Errorf("%s is still loaded in launchd after the test", label)
			}
			if _, err := os.Stat(filepath.Join(it.Agents, label+".plist")); err == nil {
				t.Errorf("leftover test plist: %s.plist", label)
			}
			if tmp, _ := filepath.Glob(filepath.Join(it.Agents, "."+label+".plist.rtm-tmp-*")); len(tmp) != 0 { // WritePlist temp files
				t.Errorf("leftover temp files: %v", tmp)
			}
			if left, _ := filepath.Glob(filepath.Join(backups, label+"-*")); len(left) != 0 {
				t.Errorf("leftover test backups: %v", left)
			}
		}
		// bootout signals the job and returns; the process may need a moment to exit. Wait for it,
		// but a process that is still there after 5 seconds is a real leftover.
		if it.sleepN != 0 {
			var pids string
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
				out, _ := osexec.Command("pgrep", "-f", fmt.Sprintf("sleep %d", it.sleepN)).Output()
				if pids = strings.TrimSpace(string(out)); pids == "" || time.Now().After(deadline) {
					break
				}
			}
			if pids != "" {
				t.Errorf("leftover test process (sleep %d) 5s after bootout: pid %s", it.sleepN, pids)
			}
		}
	})
	return it
}

// Create writes dev.rtm.test.<name>.plist and arranges its removal. script runs under /bin/sh -c;
// extra is raw plist XML for more keys.
func (e *Env) Create(t *testing.T, name, script, extra string) string {
	t.Helper()
	label := launchd.TestPrefix + name
	path := filepath.Join(e.Agents, label+".plist")
	e.labels = append(e.labels, label)

	// Leftovers of an aborted earlier run. A failing bootout is normal when nothing is loaded,
	// but then launchctl must agree that nothing is loaded.
	launchd.Bootout(e.Env.Runner, e.Env.UID, label)
	if e.loaded(label) {
		t.Fatalf("%s is still loaded after bootout; unload it by hand: launchctl bootout gui/%d/%s", label, e.Env.UID, label)
	}
	os.Remove(path)

	t.Cleanup(func() {
		// An error is fine only if the job is not loaded anyway (it was never loaded, or an
		// earlier step already unloaded it). If it is still loaded, that is a failure.
		if err := launchd.Bootout(e.Env.Runner, e.Env.UID, label); err != nil && e.loaded(label) {
			t.Errorf("bootout %s failed and the job is still loaded: %v", label, err)
		}
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
`, label, html.EscapeString(script), filepath.Join(e.Logs, name+".out"), filepath.Join(e.Logs, name+".err"), extra)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := osexec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %s", out)
	}
	return label
}
