package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const runLabel = "dev.rtm.test.runme"

func prn(state string, pid, runs int, exit string) sys.Result {
	s := fmt.Sprintf("x = {\n\tstate = %s\n\truns = %d\n", state, runs)
	if pid > 0 {
		s += fmt.Sprintf("\tpid = %d\n", pid)
	}
	if exit != "" {
		s += "\tlast exit code = " + exit + "\n"
	}
	return sys.Result{Out: s + "}\n"}
}

// runEnv has a plist (with a log file) for runLabel; `launchctl print` replies come from prints
// in order, every mutating command succeeds. The first lookup() consumes one reply, then Run.
func runEnv(t *testing.T, prints ...sys.Result) (sys.Env, *sys.FakeRunner, string) {
	env := m2Env(t)
	logf := filepath.Join(t.TempDir(), "o.log")
	os.WriteFile(logf, []byte("hello\nworld\n"), 0o644)
	writePlist(t, env, runLabel, "<key>StandardOutPath</key><string>"+logf+"</string>")
	fr := env.Runner.(*sys.FakeRunner)
	plist := filepath.Join(env.Home, "Library", "LaunchAgents", runLabel+".plist")
	fr.Results["launchctl kickstart gui/501/"+runLabel] = sys.Result{}
	fr.Results["launchctl kickstart -k gui/501/"+runLabel] = sys.Result{}
	fr.Results["launchctl bootstrap gui/501 "+plist] = sys.Result{}
	fr.Queue = map[string][]sys.Result{"launchctl print gui/501/" + runLabel: prints}
	return env, fr, logf
}

func mutations(fr *sys.FakeRunner) []string {
	var m []string
	for _, c := range fr.Calls {
		if strings.HasPrefix(c, "launchctl kickstart") || strings.HasPrefix(c, "launchctl bootstrap") || strings.HasPrefix(c, "launchctl bootout") {
			m = append(m, c)
		}
	}
	return m
}

func TestRunSucceeds(t *testing.T) {
	env, fr, logf := runEnv(t,
		prn("not running", 0, 0, ""),  // lookup
		prn("not running", 0, 0, ""),  // Run: before
		prn("not running", 0, 1, "0")) // first poll: done
	code, out, errw := exec(t, env, "run", runLabel, "--wait", "2s")
	if code != 0 || errw != "" {
		t.Fatalf("code=%d err=%q out=%s", code, errw, out)
	}
	for _, want := range []string{"started " + runLabel, "finished: exit 0 (runs 1)", "log " + logf + ":", "  | hello", "  | world"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if m := mutations(fr); len(m) != 1 || m[0] != "launchctl kickstart gui/501/"+runLabel {
		t.Errorf("mutations = %v", m)
	}
}

func TestRunFailingJobExitsNonZero(t *testing.T) {
	env, _, _ := runEnv(t, prn("not running", 0, 0, ""), prn("not running", 0, 0, ""), prn("not running", 0, 1, "1"))
	code, out, errw := exec(t, env, "run", runLabel, "--wait", "2s")
	if code != 1 || !strings.Contains(errw, runLabel+" exited with 1") || !strings.Contains(out, "finished: exit 1 (runs 1)") {
		t.Fatalf("code=%d err=%q out=%s", code, errw, out)
	}
}

func TestRunNotLoaded(t *testing.T) {
	env, fr, _ := runEnv(t, notLoadedReply, notLoadedReply)
	code, _, errw := exec(t, env, "run", runLabel)
	if code != 1 || !strings.Contains(errw, "--load") || len(mutations(fr)) != 0 {
		t.Fatalf("code=%d err=%q mutations=%v", code, errw, mutations(fr))
	}

	env, fr, _ = runEnv(t, notLoadedReply, notLoadedReply, prn("not running", 0, 0, ""), prn("not running", 0, 1, "0"))
	code, out, errw := exec(t, env, "run", "--load", runLabel, "--wait", "2s") // flags before the label
	if code != 0 || !strings.Contains(out, "(registered with launchd first)") {
		t.Fatalf("code=%d err=%q out=%s", code, errw, out)
	}
	if m := mutations(fr); len(m) != 2 || !strings.Contains(m[0], "bootstrap") || !strings.Contains(m[1], "kickstart") {
		t.Errorf("mutations = %v", m)
	}
}

var notLoadedReply = sys.Result{Err: fmt.Errorf("exit status 113")}

func TestRunRefusesRunningJob(t *testing.T) {
	env, fr, _ := runEnv(t, prn("running", 42, 1, ""), prn("running", 42, 1, ""))
	code, _, errw := exec(t, env, "run", runLabel)
	if code != 1 || !strings.Contains(errw, "already running (pid 42)") || len(mutations(fr)) != 0 {
		t.Fatalf("code=%d err=%q mutations=%v", code, errw, mutations(fr))
	}
}

func TestRunRestart(t *testing.T) {
	env, fr, _ := runEnv(t, prn("running", 42, 1, ""), prn("running", 42, 1, ""), prn("not running", 0, 2, "0"))
	code, out, errw := exec(t, env, "run", runLabel, "--restart", "--wait", "2s")
	if code != 0 || !strings.Contains(out, "finished: exit 0 (runs 2)") {
		t.Fatalf("code=%d err=%q out=%s", code, errw, out)
	}
	if m := mutations(fr); len(m) != 1 || !strings.Contains(m[0], "kickstart -k") {
		t.Errorf("mutations = %v", m)
	}
}

func TestRunStillRunningAfterWait(t *testing.T) {
	env, fr, _ := runEnv(t, prn("not running", 0, 0, ""), prn("not running", 0, 0, ""))
	fr.Results["launchctl print gui/501/"+runLabel] = prn("running", 77, 1, "")
	code, out, _ := exec(t, env, "run", runLabel, "--wait", "10ms")
	if code != 0 || !strings.Contains(out, "no result after 10ms (state: running, pid 77)") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestRunErrors(t *testing.T) {
	env, fr, _ := runEnv(t)
	for name, args := range map[string][]string{
		"no label":      {"run"},
		"two labels":    {"run", "a", "b"},
		"unknown label": {"run", "nope"},
		"cron":          {"run", "cron:0ec33761"},
		"negative wait": {"run", runLabel, "--wait", "-1s"},
		"unknown flag":  {"run", runLabel, "--bogus"},
	} {
		if code, _, _ := exec(t, env, args...); code == 0 {
			t.Errorf("%s: want failure", name)
		}
	}
	if m := mutations(fr); len(m) != 0 {
		t.Errorf("error paths must not change anything: %v", m)
	}
}
