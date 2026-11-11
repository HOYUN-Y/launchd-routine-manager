package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/testutil"
)

func exec(t *testing.T, env sys.Env, args ...string) (code int, out, errw string) {
	var o, e bytes.Buffer
	code = run(context.Background(), args, &o, &e, env)
	return code, o.String(), e.String()
}

func testEnv(t *testing.T) sys.Env {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{"crontab -l": {Out: testutil.Crontab(t)}}}
	return sys.Env{Home: testutil.Home(t), SystemAgentsDir: t.TempDir(), Runner: fr}
}

func TestListDefaultIsAIOnly(t *testing.T) {
	code, out, _ := exec(t, testEnv(t), "list")
	if code != 0 || strings.Contains(out, "unrelated") || strings.Contains(out, "cron:e89e38a4") ||
		!strings.Contains(out, "claude-direct") || !strings.Contains(out, "cron:0ec33761") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestListAllJSON(t *testing.T) {
	code, out, errw := exec(t, testEnv(t), "list", "--all", "--json")
	if code != 0 || errw != "" {
		t.Fatalf("code=%d stderr=%q", code, errw)
	}
	var rs []routine.Routine
	if err := json.Unmarshal([]byte(out), &rs); err != nil || len(rs) != 6 {
		t.Fatalf("err=%v len=%d\n%s", err, len(rs), out)
	}
	if !strings.Contains(out, "2>&1") {
		t.Error("command text should not be HTML-escaped")
	}
	for _, key := range []string{`"label"`, `"source"`, `"ai"`, `"tracked"`, `"evidence"`, `"command"`} {
		if !strings.Contains(out, key) {
			t.Errorf("missing snake_case key %s", key)
		}
	}
}

func TestListJSONEmptyIsArray(t *testing.T) {
	env := testEnv(t)
	env.Home = t.TempDir()
	env.Runner = &sys.FakeRunner{Results: map[string]sys.Result{"crontab -l": {Err: errors.New("crontab: no crontab for me")}}}
	if _, out, _ := exec(t, env, "list", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("out = %q", out)
	}
}

func TestTrackUntrack(t *testing.T) {
	env := testEnv(t)
	if code, _, errw := exec(t, env, "track", "nope"); code != 1 || !strings.Contains(errw, "unknown label") {
		t.Fatalf("unknown label: code=%d %q", code, errw)
	}
	if code, _, _ := exec(t, env, "track", "dev.rtm.test.unrelated"); code != 0 {
		t.Fatal("track failed")
	}
	if _, out, _ := exec(t, env, "list"); !strings.Contains(out, "dev.rtm.test.unrelated") || !strings.Contains(out, "ai (tracked)") {
		t.Fatalf("tracked item should be listed:\n%s", out)
	}
	if _, out, _ := exec(t, env, "track", "dev.rtm.test.unrelated"); !strings.Contains(out, "already") {
		t.Fatalf("out = %q", out)
	}
	if code, _, _ := exec(t, env, "untrack", "dev.rtm.test.unrelated"); code != 0 {
		t.Fatal("untrack failed")
	}
	if _, out, _ := exec(t, env, "list"); strings.Contains(out, "dev.rtm.test.unrelated") {
		t.Fatalf("untracked item should be gone:\n%s", out)
	}
}

func TestUsage(t *testing.T) {
	if code, _, errw := exec(t, testEnv(t)); code != 2 || !strings.Contains(errw, "usage") {
		t.Fatalf("code=%d", code)
	}
	if code, _, _ := exec(t, testEnv(t), "bogus"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

// An error the TUI hands back (a schedule change that failed while the user was quitting) must
// reach stderr and make the exit code non-zero; a clean return exits 0.
func TestLaunchTUIExitCode(t *testing.T) {
	defer func(old func(context.Context, sys.Env) error) { tuiRun = old }(tuiRun)
	var errw bytes.Buffer
	tuiRun = func(context.Context, sys.Env) error { return errors.New("x.y: launchd NOT loaded; backup at /b") }
	if code := launchTUI(context.Background(), sys.Env{}, &errw); code != 1 || !strings.Contains(errw.String(), "rtm: x.y: launchd NOT loaded; backup at /b") {
		t.Errorf("code=%d stderr=%q", code, errw.String())
	}
	errw.Reset()
	tuiRun = func(context.Context, sys.Env) error { return nil }
	if code := launchTUI(context.Background(), sys.Env{}, &errw); code != 0 || errw.Len() != 0 {
		t.Errorf("code=%d stderr=%q", code, errw.String())
	}
}
