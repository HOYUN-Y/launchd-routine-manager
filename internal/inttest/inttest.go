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

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

type Env struct {
	Env    sys.Env
	Agents string // ~/Library/LaunchAgents
	Logs   string // temp dir for the jobs' stdout/stderr
	labels []string
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
		for _, label := range it.labels {
			if it.loaded(label) {
				t.Errorf("%s is still loaded in launchd after the test", label)
			}
		}
		left, _ := filepath.Glob(filepath.Join(it.Agents, launchd.TestPrefix+"*"))
		hidden, _ := filepath.Glob(filepath.Join(it.Agents, ".dev.rtm.test.*")) // WritePlist temp files
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
