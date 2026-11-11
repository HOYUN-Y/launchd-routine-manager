package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

func env(t *testing.T, res map[string]sys.Result) sys.Env {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755)
	os.WriteFile(filepath.Join(home, "Library", "LaunchAgents", "a.plist"), nil, 0o644)
	return sys.Env{Home: home, UID: os.Getuid(), SystemAgentsDir: t.TempDir(), Runner: &sys.FakeRunner{Results: res}}
}

func byName(cs []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range cs {
		m[c.Name] = c
	}
	return m
}

func TestRunAllGood(t *testing.T) {
	cs := Run(env(t, map[string]sys.Result{
		"zsh -lc command -v claude": {Out: "welcome banner from .zshrc\n/opt/x/bin/claude\n"},
		"zsh -lc command -v codex":  {Out: "/opt/homebrew/bin/codex\n"},
		"launchctl managername":     {Out: "Aqua\n"},
	}))
	m := byName(cs)
	if m["claude"].Status != OK || m["claude"].Detail != "/opt/x/bin/claude" {
		t.Errorf("claude = %+v (rc-file noise must be ignored)", m["claude"])
	}
	if m["codex"].Status != OK || m["launchctl"].Status != OK || m["user agents"].Status != OK || m["system agents"].Status != OK || m["config"].Status != OK {
		t.Errorf("checks = %+v", cs)
	}
	if Failed(cs) {
		t.Error("nothing should fail")
	}
}

func TestMissingBinariesAreOnlyWarnings(t *testing.T) {
	cs := Run(env(t, map[string]sys.Result{
		"zsh -lc command -v claude": {Err: errors.New("exit status 1")},
		"zsh -lc command -v codex":  {Out: "codex not found\n"}, // exit 0 but no path
		"launchctl managername":     {Out: "Aqua\n"},
	}))
	m := byName(cs)
	if m["claude"].Status != Warn || m["codex"].Status != Warn {
		t.Errorf("claude=%+v codex=%+v", m["claude"], m["codex"])
	}
	if Failed(cs) {
		t.Error("missing claude/codex must not fail doctor (PLAN §10)")
	}
}

func TestLaunchctlProblems(t *testing.T) {
	base := map[string]sys.Result{"zsh -lc command -v claude": {}, "zsh -lc command -v codex": {}}
	base["launchctl managername"] = sys.Result{Err: errors.New("boom")}
	cs := Run(env(t, base))
	if byName(cs)["launchctl"].Status != Fail || !Failed(cs) {
		t.Errorf("launchctl error must fail: %+v", byName(cs)["launchctl"])
	}
	base["launchctl managername"] = sys.Result{Out: "Background\n"}
	if c := byName(Run(env(t, base)))["launchctl"]; c.Status != Warn {
		t.Errorf("non-Aqua session = %+v", c)
	}
}
