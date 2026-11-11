package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

var pinned = time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)

// m2Env pins the clock and uid, and makes `launchctl print` for the given labels return the
// running.txt sample. Every other label "fails" (= not loaded).
func m2Env(t *testing.T, loaded ...string) sys.Env {
	env := testEnv(t)
	env.UID, env.Now = 501, func() time.Time { return pinned }
	b, err := os.ReadFile("../../testdata/launchctl/running.txt")
	if err != nil {
		t.Fatal(err)
	}
	fr := env.Runner.(*sys.FakeRunner)
	for _, l := range loaded {
		fr.Results[fmt.Sprintf("launchctl print gui/501/%s", l)] = sys.Result{Out: string(b)}
	}
	return env
}

func writePlist(t *testing.T, env sys.Env, label, extra string) {
	t.Helper()
	body := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>/usr/bin/true</string></array>%s</dict></plist>`, label, extra)
	if err := os.WriteFile(filepath.Join(env.Home, "Library", "LaunchAgents", label+".plist"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListShowsStateAndNext(t *testing.T) {
	env := m2Env(t, "dev.rtm.test.claude-direct")
	code, out, errw := exec(t, env, "list")
	if code != 0 || errw != "" {
		t.Fatalf("code=%d err=%q", code, errw)
	}
	for _, want := range []string{"SCHEDULE", "STATE", "NEXT", "daily 09:00", "running", "tomorrow 09:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	_, all, _ := exec(t, env, "list", "--all")
	if !strings.Contains(all, "not loaded") {
		t.Errorf("unloaded plist should say so:\n%s", all)
	}
}

func TestShowText(t *testing.T) {
	code, out, _ := exec(t, m2Env(t, "dev.rtm.test.claude-direct"), "show", "dev.rtm.test.claude-direct")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{
		"label:      dev.rtm.test.claude-direct",
		"class:      AI\n            - direct: /opt/homebrew/bin/claude\n", // evidence line: indented, no stray colon
		"schedule:   daily 09:00", "next run:   2026-11-13 09:00:00",
		"state:      running (pid 4859)", "runs:       1", "last exit:  never exited",
		"command:    /opt/homebrew/bin/claude -p /changelog-update",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestShowJSONFlagAfterLabel(t *testing.T) {
	code, out, errw := exec(t, m2Env(t, "dev.rtm.test.claude-direct"), "show", "dev.rtm.test.claude-direct", "--json")
	if code != 0 || errw != "" {
		t.Fatalf("code=%d err=%q", code, errw)
	}
	var r routine.Routine
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.Status == nil || r.Status.PID != 4859 || !r.Status.Loaded || r.NextRun == nil {
		t.Errorf("r = %+v", r)
	}
	for _, key := range []string{`"status"`, `"next_run"`, `"pid"`} { // last_exit_code is omitted: never exited
		if !strings.Contains(out, key) {
			t.Errorf("missing %s", key)
		}
	}
}

func TestShowNotLoadedAndErrors(t *testing.T) {
	env := m2Env(t)
	if code, out, _ := exec(t, env, "show", "dev.rtm.test.unrelated"); code != 0 || !strings.Contains(out, "state:      not loaded") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if code, _, errw := exec(t, env, "show", "nope"); code != 1 || !strings.Contains(errw, "unknown label") {
		t.Fatalf("code=%d %q", code, errw)
	}
	if code, _, errw := exec(t, env, "show"); code != 1 || !strings.Contains(errw, "usage") {
		t.Fatalf("code=%d %q", code, errw)
	}
	if code, out, _ := exec(t, env, "show", "cron:0ec33761"); code != 0 || !strings.Contains(out, "n/a (crontab keeps no state)") {
		t.Fatalf("cron show: code=%d\n%s", code, out)
	}
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestLogsLastN(t *testing.T) {
	env := m2Env(t)
	logf := filepath.Join(t.TempDir(), "o.log")
	os.WriteFile(logf, []byte(numbered(120)), 0o644)
	writePlist(t, env, "dev.rtm.test.logs", fmt.Sprintf("<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>", logf, logf))

	_, out, _ := exec(t, env, "logs", "dev.rtm.test.logs", "-n", "3") // label first, flags after
	if out != "line 118\nline 119\nline 120\n" {
		t.Fatalf("out = %q (same file for stdout/stderr: no header, printed once)", out)
	}
	_, out, _ = exec(t, env, "logs", "-n", "2", "dev.rtm.test.logs") // flags first
	if out != "line 119\nline 120\n" {
		t.Fatalf("out = %q", out)
	}
	if _, out, _ = exec(t, env, "logs", "dev.rtm.test.logs"); strings.Count(out, "\n") != 50 {
		t.Errorf("default is 50 lines, got %d", strings.Count(out, "\n"))
	}
}

func TestLogsTwoFilesAndMissing(t *testing.T) {
	env := m2Env(t)
	dir := t.TempDir()
	o, e := filepath.Join(dir, "o.log"), filepath.Join(dir, "e.log")
	os.WriteFile(o, []byte("out1\nout2\n"), 0o644)
	writePlist(t, env, "dev.rtm.test.two", fmt.Sprintf("<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>", o, e))
	_, out, _ := exec(t, env, "logs", "dev.rtm.test.two", "-n", "1")
	want := fmt.Sprintf("==> %s <==\nout2\n==> %s <==\n(log file does not exist yet: %s)\n", o, e, e)
	if out != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}
}

func TestLogsNoPath(t *testing.T) {
	code, _, errw := exec(t, m2Env(t), "logs", "dev.rtm.test.claude-direct")
	if code != 1 || !strings.Contains(errw, "no log path") {
		t.Fatalf("code=%d %q", code, errw)
	}
	if code, _, _ := exec(t, m2Env(t), "logs", "nope"); code != 1 {
		t.Fatal("unknown label must fail")
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestLogsFollow(t *testing.T) {
	followEvery = 10 * time.Millisecond
	defer func() { followEvery = 500 * time.Millisecond }()
	env := m2Env(t)
	logf := filepath.Join(t.TempDir(), "f.log")
	os.WriteFile(logf, []byte("old1\nold2\n"), 0o644)
	writePlist(t, env, "dev.rtm.test.follow", "<key>StandardOutPath</key><string>"+logf+"</string>")

	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuf{}
	done := make(chan int)
	go func() {
		done <- run(ctx, []string{"logs", "dev.rtm.test.follow", "-n", "1", "-f"}, out, &bytes.Buffer{}, env)
	}()

	waitFor := func(s string) {
		for i := 0; i < 300; i++ {
			if strings.Contains(out.String(), s) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q, got %q", s, out.String())
	}
	waitFor("old2")
	f, _ := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("new1\n")
	waitFor("new1")
	f.Close()
	os.WriteFile(logf, []byte("rotated\n"), 0o644) // truncated: re-read from the start
	waitFor("rotated")
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("-f did not stop on cancel")
	}
	if strings.Contains(out.String(), "old1") {
		t.Errorf("-n 1 must not print old1: %q", out.String())
	}
}

func TestDoctorCLI(t *testing.T) {
	env := m2Env(t)
	fr := env.Runner.(*sys.FakeRunner)
	fr.Results["zsh -lc command -v claude"] = sys.Result{Out: "/opt/x/claude\n"}
	fr.Results["zsh -lc command -v codex"] = sys.Result{Err: errors.New("exit status 1")}
	fr.Results["launchctl managername"] = sys.Result{Out: "Aqua\n"}

	code, out, _ := exec(t, env, "doctor")
	if code != 0 || !strings.Contains(out, "[ok  ] claude") || !strings.Contains(out, "[warn] codex") || !strings.Contains(out, "uid=501") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out, _ = exec(t, env, "doctor", "--json")
	var checks []map[string]string
	if code != 0 || json.Unmarshal([]byte(out), &checks) != nil || len(checks) != 7 {
		t.Fatalf("code=%d len=%d\n%s", code, len(checks), out)
	}

	fr.Results["launchctl managername"] = sys.Result{Err: errors.New("boom")}
	if code, out, _ := exec(t, env, "doctor"); code != 1 || !strings.Contains(out, "[fail] launchctl") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

// Safety rule: M2 runs only read-only commands, whatever the subcommand.
func TestOnlyReadOnlyCommands(t *testing.T) {
	env := m2Env(t, "dev.rtm.test.claude-direct")
	fr := env.Runner.(*sys.FakeRunner)
	fr.Results["zsh -lc command -v claude"] = sys.Result{}
	fr.Results["zsh -lc command -v codex"] = sys.Result{}
	fr.Results["launchctl managername"] = sys.Result{Out: "Aqua\n"}
	for _, args := range [][]string{{"list", "--all"}, {"show", "dev.rtm.test.claude-direct"}, {"doctor"}, {"logs", "dev.rtm.test.claude-direct"}} {
		exec(t, env, args...)
	}
	for _, c := range fr.Calls {
		ok := c == "crontab -l" || c == "launchctl managername" || strings.HasPrefix(c, "launchctl print ") || strings.HasPrefix(c, "zsh -lc command -v ")
		if !ok {
			t.Errorf("non-read-only command ran: %q", c)
		}
	}
}

// A plist rtm cannot use: warned about by `list` and `doctor`, never by `show`/`logs`.
func TestWarningsOnlyInListAndDoctor(t *testing.T) {
	env := m2Env(t, "dev.rtm.test.claude-direct")
	bad := filepath.Join(env.Home, "Library", "LaunchAgents", "empty.plist")
	os.WriteFile(bad, []byte(`<?xml version="1.0"?><plist version="1.0"><dict/></plist>`), 0o644)
	fr := env.Runner.(*sys.FakeRunner)
	fr.Results["zsh -lc command -v claude"] = sys.Result{}
	fr.Results["zsh -lc command -v codex"] = sys.Result{}
	fr.Results["launchctl managername"] = sys.Result{Out: "Aqua\n"}

	if _, _, errw := exec(t, env, "list"); !strings.Contains(errw, "empty.plist: no Label") {
		t.Errorf("list should warn: %q", errw)
	}
	if _, out, _ := exec(t, env, "doctor"); !strings.Contains(out, "[warn] scan") || !strings.Contains(out, "empty.plist: no Label") {
		t.Errorf("doctor should report the skipped file:\n%s", out)
	}
	for _, args := range [][]string{
		{"show", "dev.rtm.test.claude-direct"}, {"show", "dev.rtm.test.claude-direct", "--json"},
		{"logs", "dev.rtm.test.claude-direct"}, {"show", "nope"},
	} {
		if _, _, errw := exec(t, env, args...); strings.Contains(errw, "warning") || strings.Contains(errw, "empty.plist") {
			t.Errorf("%v printed a scan warning: %q", args, errw)
		}
	}
}

// A broken config.json is a warning for readers, and an error (without touching the file) for track.
func TestBrokenConfig(t *testing.T) {
	env := m2Env(t)
	cfg := filepath.Join(env.Home, ".config", "rtm", "config.json")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	broken := `{"tracked": ["a"`
	os.WriteFile(cfg, []byte(broken), 0o644)

	if code, _, errw := exec(t, env, "list", "--all"); code != 0 || !strings.Contains(errw, "warning: config:") {
		t.Errorf("list: code=%d %q", code, errw)
	}
	if code, _, errw := exec(t, env, "track", "dev.rtm.test.unrelated"); code != 1 || !strings.Contains(errw, "will not overwrite") {
		t.Errorf("track: code=%d %q", code, errw)
	}
	if code, _, _ := exec(t, env, "untrack", "dev.rtm.test.unrelated"); code != 1 {
		t.Errorf("untrack on a broken file must fail, got %d", code)
	}
	if b, _ := os.ReadFile(cfg); string(b) != broken {
		t.Errorf("config was modified: %s", b)
	}
}

func TestTrackBracketLabelEndToEnd(t *testing.T) {
	env := m2Env(t)
	writePlist(t, env, "dev.rtm.test.a]b", "")
	writePlist(t, env, "dev.rtm.test.b", "")
	for _, l := range []string{"dev.rtm.test.a]b", "dev.rtm.test.b"} {
		if code, _, errw := exec(t, env, "track", l); code != 0 {
			t.Fatalf("track %s: %q", l, errw)
		}
	}
	_, out, _ := exec(t, env, "list", "--json")
	if !strings.Contains(out, `"dev.rtm.test.a]b"`) || !strings.Contains(out, `"dev.rtm.test.b"`) {
		t.Errorf("both labels must be tracked:\n%s", out)
	}
}

// Log rotation: the old file is renamed away and a NEW file takes its place. If the new file is
// already longer than our offset, comparing sizes alone would skip its beginning.
func TestLogsFollowSeesReplacedFile(t *testing.T) {
	followEvery = 10 * time.Millisecond
	defer func() { followEvery = 500 * time.Millisecond }()
	env := m2Env(t)
	dir := t.TempDir()
	logf := filepath.Join(dir, "f.log")
	os.WriteFile(logf, []byte("old1\nold2\n"), 0o644) // 10 bytes
	writePlist(t, env, "dev.rtm.test.rotate", "<key>StandardOutPath</key><string>"+logf+"</string>")

	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuf{}
	done := make(chan int)
	go func() { done <- run(ctx, []string{"logs", "dev.rtm.test.rotate", "-f"}, out, &bytes.Buffer{}, env) }()
	wait := func(s string) {
		for i := 0; i < 300; i++ {
			if strings.Contains(out.String(), s) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q, got %q", s, out.String())
	}
	wait("old2")

	os.Rename(logf, logf+".1") // rotate
	os.WriteFile(logf, []byte("new-line-1\nnew-line-2\nnew-line-3\n"), 0o644)
	wait("new-line-1") // lost before the fix: the new file is 33 bytes > the old offset of 10
	wait("new-line-3")
	cancel()
	<-done
	if got := strings.Count(out.String(), "new-line-1"); got != 1 {
		t.Errorf("new-line-1 printed %d times: %q", got, out.String())
	}
}

func TestLogsFollowFileAppearsLater(t *testing.T) {
	followEvery = 10 * time.Millisecond
	defer func() { followEvery = 500 * time.Millisecond }()
	env := m2Env(t)
	logf := filepath.Join(t.TempDir(), "later.log")
	writePlist(t, env, "dev.rtm.test.later", "<key>StandardOutPath</key><string>"+logf+"</string>")
	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuf{}
	done := make(chan int)
	go func() { done <- run(ctx, []string{"logs", "dev.rtm.test.later", "-f"}, out, &bytes.Buffer{}, env) }()
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(logf, []byte("first-line\n"), 0o644)
	for i := 0; i < 300 && !strings.Contains(out.String(), "first-line"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(out.String(), "does not exist yet") || !strings.Contains(out.String(), "first-line") {
		t.Errorf("out = %q", out.String())
	}
}

func TestLogsKeepsTrailingBlankLines(t *testing.T) {
	env := m2Env(t)
	logf := filepath.Join(t.TempDir(), "b.log")
	os.WriteFile(logf, []byte("a\nb\n\n\n"), 0o644)
	writePlist(t, env, "dev.rtm.test.blank", "<key>StandardOutPath</key><string>"+logf+"</string>")
	if _, out, _ := exec(t, env, "logs", "dev.rtm.test.blank", "-n", "3"); out != "b\n\n\n" {
		t.Errorf("out = %q, want the two blank lines kept", out)
	}
}

// StartInterval's next run is a guess from the log mtime: say so, in text and in JSON.
func TestEstimatedNextRunIsLabelled(t *testing.T) {
	env := m2Env(t, "dev.rtm.test.claude-direct")
	logf := filepath.Join(t.TempDir(), "i.log")
	os.WriteFile(logf, []byte("x\n"), 0o644)
	mt := pinned.Add(-30 * time.Minute)
	os.Chtimes(logf, mt, mt)
	writePlist(t, env, "dev.rtm.test.interval", "<key>StartInterval</key><integer>3600</integer><key>StandardOutPath</key><string>"+logf+"</string>")

	_, out, _ := exec(t, env, "show", "dev.rtm.test.interval")
	if !strings.Contains(out, "next run:   2026-11-12 11:00:00 (estimated)") {
		t.Errorf("show:\n%s", out)
	}
	_, out, _ = exec(t, env, "show", "dev.rtm.test.interval", "--json")
	if !strings.Contains(out, `"next_run_estimated": true`) {
		t.Errorf("json:\n%s", out)
	}
	_, out, _ = exec(t, env, "list", "--all")
	if !strings.Contains(out, "11:00 (estimated)") {
		t.Errorf("list:\n%s", out)
	}

	// a calendar schedule is exact: no label, no JSON flag
	_, out, _ = exec(t, env, "show", "dev.rtm.test.claude-direct")
	if !strings.Contains(out, "next run:   2026-11-13 09:00:00\n") {
		t.Errorf("exact time must not be labelled:\n%s", out)
	}
	if !strings.Contains(out, "last run:   -\n") {
		t.Errorf("no log, no last run, no explanation:\n%s", out)
	}
	_, out, _ = exec(t, env, "show", "dev.rtm.test.claude-direct", "--json")
	if strings.Contains(out, "next_run_estimated") {
		t.Errorf("json:\n%s", out)
	}
}
