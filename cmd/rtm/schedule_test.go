package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const schedLabel = "dev.rtm.test.sched"

// schedEnv has dev.rtm.test.sched (StartInterval 1h) in a temp home. Commands the test does not
// script fail, so nothing else can run.
func schedEnv(t *testing.T, prints ...sys.Result) (sys.Env, *sys.FakeRunner, string) {
	env := m2Env(t)
	writePlist(t, env, schedLabel, "<key>StartInterval</key><integer>3600</integer>")
	path := filepath.Join(env.Home, "Library", "LaunchAgents", schedLabel+".plist")
	fr := env.Runner.(*sys.FakeRunner)
	fr.Results["plutil -lint "+path] = sys.Result{}
	fr.Results["launchctl bootout gui/501/"+schedLabel] = sys.Result{}
	fr.Results["launchctl bootstrap gui/501 "+path] = sys.Result{}
	fr.Queue = map[string][]sys.Result{"launchctl print gui/501/" + schedLabel: prints}
	return env, fr, path
}

func loadedCLI(interval int, calendarHour int) sys.Result {
	s := fmt.Sprintf("x = {\n\tstate = not running\n\truns = 1\n")
	if interval > 0 {
		s += fmt.Sprintf("\trun interval = %d seconds\n", interval)
	}
	if calendarHour >= 0 {
		s += fmt.Sprintf("\tevent triggers = {\n\t\tt => {\n\t\t\tstream = com.apple.launchd.calendarinterval\n\t\t\tdescriptor = {\n\t\t\t\t\"Minute\" => 0\n\t\t\t\t\"Hour\" => %d\n\t\t\t}\n\t\t}\n\t}\n", calendarHour)
	}
	return sys.Result{Out: s + "}\n"}
}

var gone = sys.Result{Err: errors.New("exit status 113")}

func TestScheduleUsageErrors(t *testing.T) {
	env, fr, path := schedEnv(t)
	before, _ := os.ReadFile(path)
	for name, args := range map[string][]string{
		"no mode":               {"schedule", schedLabel},
		"no label":              {"schedule", "--daily", "09:00"},
		"two modes":             {"schedule", schedLabel, "--daily", "09:00", "--every", "30m"},
		"daily and revert":      {"schedule", schedLabel, "--daily", "09:00", "--revert"},
		"weekly without time":   {"schedule", schedLabel, "--weekly", "mon,wed"},
		"daily with extra word": {"schedule", schedLabel, "--daily", "09:00", "extra"},
		"two labels":            {"schedule", schedLabel, "other", "--daily", "09:00"},
	} {
		code, _, errw := exec(t, env, args...)
		if code != 1 || !strings.Contains(errw, "usage: rtm schedule") {
			t.Errorf("%s: code=%d err=%q", name, code, errw)
		}
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"bad time":     {[]string{"schedule", schedLabel, "--daily", "25:00"}, "bad time"},
		"bad weekday":  {[]string{"schedule", schedLabel, "--weekly", "mon,xyz", "09:00"}, "bad weekday"},
		"bad interval": {[]string{"schedule", schedLabel, "--every", "10s"}, "1m minimum"},
		"unknown":      {[]string{"schedule", "nope", "--daily", "09:00"}, "unknown label"},
	} {
		if code, _, errw := exec(t, env, c.args...); code != 1 || !strings.Contains(errw, c.want) {
			t.Errorf("%s: code=%d err=%q", name, code, errw)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("usage errors must not touch the plist")
	}
	for _, c := range fr.Calls {
		if strings.Contains(c, "bootout") || strings.Contains(c, "plutil") {
			t.Errorf("nothing may run on a usage error: %q", c)
		}
	}
}

func TestScheduleNotLoaded(t *testing.T) {
	env, _, path := schedEnv(t, gone, gone) // lookup, then the schedule change
	code, out, errw := exec(t, env, "schedule", schedLabel, "--daily", "9:05")
	if code != 0 || errw != "" {
		t.Fatalf("code=%d err=%q", code, errw)
	}
	for _, want := range []string{schedLabel + ": every 1h -> daily 09:05", "backup: ", "verified: plist file holds daily 09:05"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "reloaded: launchctl") {
		t.Errorf("an unloaded job is not reloaded:\n%s", out)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "StartCalendarInterval") {
		t.Error("plist not updated")
	}
}

func TestScheduleReloads(t *testing.T) {
	// prints: lookup, the first look, the look right before bootout, the confirmation
	env, fr, _ := schedEnv(t, loadedCLI(3600, -1), loadedCLI(3600, -1), loadedCLI(3600, -1), loadedCLI(0, 5))
	code, out, errw := exec(t, env, "schedule", schedLabel, "--daily", "05:00")
	if code != 0 {
		t.Fatalf("code=%d err=%q\n%s", code, errw, out)
	}
	for _, want := range []string{"reloaded: launchctl bootout + bootstrap", "verified: launchd now holds daily 05:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	joined := strings.Join(fr.Calls, "\n")
	if strings.Index(joined, "bootout") > strings.Index(joined, "bootstrap") {
		t.Errorf("bootout must come before bootstrap:\n%s", joined)
	}
}

func TestScheduleWeeklyTimeFollowsTheDays(t *testing.T) {
	env, _, path := schedEnv(t, gone, gone)
	code, out, errw := exec(t, env, "schedule", schedLabel, "--weekly", "mon,wed", "09:00")
	if code != 0 || !strings.Contains(out, "weekly mon 09:00, weekly wed 09:00") {
		t.Fatalf("code=%d err=%q\n%s", code, errw, out)
	}
	if b, _ := os.ReadFile(path); strings.Count(string(b), "<key>Weekday</key>") != 2 {
		t.Errorf("two calendar entries expected:\n%s", b)
	}
}

func TestScheduleEveryAndAt(t *testing.T) {
	env, _, _ := schedEnv(t, gone, gone)
	if code, out, _ := exec(t, env, "schedule", schedLabel, "--at", "09:00,18:00"); code != 0 || !strings.Contains(out, "daily 09:00, daily 18:00") {
		t.Fatalf("--at: code=%d\n%s", code, out)
	}
	env, _, _ = schedEnv(t, gone, gone)
	if code, out, _ := exec(t, env, "schedule", schedLabel, "--every", "30m"); code != 0 || !strings.Contains(out, "every 1h -> every 30m") {
		t.Fatalf("--every: code=%d\n%s", code, out)
	}
}

func TestScheduleSameScheduleAndRunningJob(t *testing.T) {
	env, _, _ := schedEnv(t, gone)
	if code, out, _ := exec(t, env, "schedule", schedLabel, "--every", "1h"); code != 0 || !strings.Contains(out, "already has this schedule") {
		t.Fatalf("code=%d\n%s", code, out)
	}

	running := sys.Result{Out: "x = {\n\tstate = running\n\tpid = 99\n\truns = 1\n}\n"}
	env, _, path := schedEnv(t, running, running)
	before, _ := os.ReadFile(path)
	code, _, errw := exec(t, env, "schedule", schedLabel, "--daily", "05:00")
	if code != 1 || !strings.Contains(errw, "--yes") {
		t.Fatalf("code=%d err=%q", code, errw)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("plist must be untouched")
	}
}

func TestScheduleSystemAgentIsRefused(t *testing.T) {
	env, _, _ := schedEnv(t)
	sysPlist := filepath.Join(env.SystemAgentsDir, "dev.rtm.test.system.plist")
	os.WriteFile(sysPlist, []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>dev.rtm.test.system</string><key>StartInterval</key><integer>60</integer></dict></plist>`), 0o644)
	before, _ := os.ReadFile(sysPlist)
	code, _, errw := exec(t, env, "schedule", "dev.rtm.test.system", "--daily", "09:00")
	if code != 1 || !strings.Contains(errw, "/Library/LaunchAgents") || !strings.Contains(errw, "only reads") {
		t.Fatalf("code=%d err=%q", code, errw)
	}
	if after, _ := os.ReadFile(sysPlist); string(after) != string(before) {
		t.Fatal("a /Library plist must never be written")
	}
}

func TestScheduleFailureMentionsTheBackup(t *testing.T) {
	env, fr, path := schedEnv(t, gone, gone)
	fr.Results["plutil -lint "+path] = sys.Result{Err: errors.New("bad plist")}
	before, _ := os.ReadFile(path)
	code, _, errw := exec(t, env, "schedule", schedLabel, "--daily", "05:00")
	if code != 1 || !strings.Contains(errw, "plutil -lint rejected") || !strings.Contains(errw, "note: backup of the previous plist:") {
		t.Fatalf("code=%d err=%q", code, errw)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("plist must be restored")
	}
}

func TestScheduleRevert(t *testing.T) {
	env, _, path := schedEnv(t, gone, gone, gone, gone)
	orig, _ := os.ReadFile(path)
	if code, _, errw := exec(t, env, "schedule", schedLabel, "--daily", "05:00"); code != 0 {
		t.Fatal(errw)
	}
	code, out, errw := exec(t, env, "schedule", schedLabel, "--revert")
	if code != 0 {
		t.Fatalf("code=%d err=%q\n%s", code, errw, out)
	}
	for _, want := range []string{"daily 05:00 -> every 1h", "restored the newest backup", "pre-revert"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// the restored plist decodes to the same values as the original (byte equality is not
	// expected: the first change re-encoded the file)
	back, _ := os.ReadFile(path)
	if !strings.Contains(string(back), "<integer>3600</integer>") || strings.Contains(string(back), "StartCalendarInterval") {
		t.Errorf("not restored:\n%s\noriginal:\n%s", back, orig)
	}
	if code, _, errw := exec(t, env, "schedule", "dev.rtm.test.nobackup", "--revert"); code != 1 {
		t.Errorf("unknown label: %q", errw)
	}
}
