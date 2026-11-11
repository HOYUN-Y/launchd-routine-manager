package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/inttest"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
)

// The TUI model with the real Live backend, driving the real launchd, on dev.rtm.test.* jobs only
// (RTM_TEST_LABELS_ONLY=1 is on, so any other label would be refused). The screens are logged:
//
//	RTM_INTEGRATION=1 go test -count=1 -run TestIntegrationTUI -v ./internal/tui
func TestIntegrationTUI(t *testing.T) {
	it := inttest.New(t)
	env := it.Env
	ok := it.Create(t, "tui.ok", "echo tui-ok; exit 0", "")
	keep := it.Create(t, "tui.keep", it.Sleep(), "<key>KeepAlive</key><true/>\n")

	m := New(context.Background(), Live{Env: env})
	m.tickEvery = 50 * time.Millisecond
	m.wait = 3 * time.Second
	drain(m, m.Init())
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})

	show := func(title string) {
		t.Helper()
		t.Logf("---- %s\n%s", title, plain(m.View()))
	}
	focus := func(label string) {
		t.Helper()
		m.search.SetValue("")
		press(m, "/")
		typeText(m, label)
		press(m, "enter")
		if r, found := m.selected(); !found || r.Label != label {
			t.Fatalf("could not focus %s (selected %v)", label, r.Label)
		}
	}
	press(m, "a") // the test jobs are not AI routines
	show("all routines, read from this Mac")

	// ---- a job that exits 0
	focus(ok)
	show("filtered to " + ok)
	press(m, "r")
	if m.dlg == nil || !strings.Contains(view(m), "등록(bootstrap)") {
		t.Fatalf("an unloaded job must ask before it is registered:\n%s", view(m))
	}
	show("run: not loaded, asks first")
	press(m, "y")
	if !strings.Contains(view(m), ok+": 종료 코드 0 (runs 1)") {
		t.Fatalf("run result:\n%s", view(m))
	}
	show("after the run")
	if st := launchd.Query(env.Runner, env.UID, ok); !st.Loaded || st.LastExitCode == nil || *st.LastExitCode != 0 || st.Runs != 1 {
		t.Fatalf("launchd: %+v", st)
	}
	if !strings.Contains(it.Read("tui.ok", "out"), "tui-ok") {
		t.Errorf("log: %q", it.Read("tui.ok", "out"))
	}
	press(m, "enter")
	show("detail")
	press(m, "esc", "l")
	if m.screen != scLogs || !strings.Contains(view(m), "tui-ok") {
		t.Fatalf("logs:\n%s", view(m))
	}
	show("logs")
	press(m, "esc")

	// ---- change the schedule, then revert
	press(m, "e")
	typeText(m, "daily 05:15")
	show("schedule form")
	press(m, "enter")
	if !strings.Contains(view(m), ok+": manual → daily 05:15 (다시 로드함)") {
		t.Fatalf("schedule result:\n%s", view(m))
	}
	show("after the schedule change")
	st := launchd.Query(env.Runner, env.UID, ok)
	if len(st.Calendars) != 1 || *st.Calendars[0].Hour != 5 || *st.Calendars[0].Minute != 15 {
		t.Fatalf("launchd holds %+v", st.Calendars)
	}
	press(m, "e")
	typeText(m, "revert")
	press(m, "enter")
	if !strings.Contains(view(m), ok+": daily 05:15 → manual") {
		t.Fatalf("revert result:\n%s", view(m))
	}
	show("after revert")
	if st := launchd.Query(env.Runner, env.UID, ok); len(st.Calendars) != 0 || st.RunInterval != 0 {
		t.Fatalf("launchd still holds a schedule after the revert: %+v", st)
	}

	// ---- a KeepAlive service: every risky step asks first
	focus(keep)
	press(m, "r", "y") // not loaded: register; launchd starts a KeepAlive job by itself
	first := launchd.Query(env.Runner, env.UID, keep)
	if !first.IsRunning() {
		t.Fatalf("not running: %+v", first)
	}
	show("KeepAlive service, started (the 3s wait ended with the job still running)")
	press(m, "r")
	if m.dlg == nil || !strings.Contains(view(m), "KeepAlive 상시 서비스") {
		t.Fatalf("restarting a running KeepAlive service must ask:\n%s", view(m))
	}
	show("restart a KeepAlive service: asks first")
	press(m, "n")
	if now := launchd.Query(env.Runner, env.UID, keep); now.PID != first.PID {
		t.Fatalf("n must not restart (pid %d -> %d)", first.PID, now.PID)
	}
	press(m, "r", "y")
	second := launchd.Query(env.Runner, env.UID, keep)
	if second.PID == 0 || second.PID == first.PID {
		t.Fatalf("y must restart the service (pid %d -> %d)", first.PID, second.PID)
	}
	press(m, "e")
	typeText(m, "daily 04:44")
	press(m, "enter")
	if m.dlg == nil || !strings.Contains(view(m), "실행 중인 작업이 중단됩니다") {
		t.Fatalf("reloading a running job must ask:\n%s", view(m))
	}
	show("schedule change on a running job: asks first")
	press(m, "y")
	st = launchd.Query(env.Runner, env.UID, keep)
	if len(st.Calendars) != 1 || *st.Calendars[0].Hour != 4 || *st.Calendars[0].Minute != 44 || st.PID == second.PID {
		t.Fatalf("launchd holds %+v pid %d -> %d", st.Calendars, second.PID, st.PID)
	}
	show("after the reload")
}
