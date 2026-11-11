package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
)

var pinned = time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)

// ---- fake backend: records every call; never touches the machine

type schedCall struct {
	kind string // set | revert
	r    string
	edit schedule.Edit
	opts routine.SchedOpts
}

type fakeBE struct {
	mu       sync.Mutex
	rs       []routine.Routine
	warns    []string
	loads    int
	runs     []routine.RunOpts
	runLabel []string
	runFn    func(ctx context.Context, r routine.Routine, o routine.RunOpts) (routine.RunResult, error)
	scheds   []schedCall
	schedFn  func(c schedCall) (routine.SchedResult, error)
	canEdit  error
	tracks   []string
	untracks []string
	trackErr error
	logsText string
	logsErr  error
	logsFor  []string

	trackIn   chan struct{} // if set, Track reports each call here ...
	trackGate chan struct{} // ... and then waits for this to be closed
	schedCtx  context.Context
}

func (f *fakeBE) Load() ([]routine.Routine, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	return append([]routine.Routine(nil), f.rs...), f.warns
}

func (f *fakeBE) Run(ctx context.Context, r routine.Routine, o routine.RunOpts) (routine.RunResult, error) {
	f.mu.Lock()
	f.runs = append(f.runs, o)
	f.runLabel = append(f.runLabel, r.Label)
	fn := f.runFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, r, o)
	}
	zero := 0
	return routine.RunResult{Started: true, Finished: true, Status: launchd.Status{Loaded: true, Runs: 1, LastExitCode: &zero}}, nil
}

func (f *fakeBE) CanEditSchedule(routine.Routine) error { return f.canEdit }

func (f *fakeBE) sched(c schedCall) (routine.SchedResult, error) {
	f.mu.Lock()
	f.scheds = append(f.scheds, c)
	fn := f.schedFn
	f.mu.Unlock()
	if fn != nil {
		return fn(c)
	}
	return routine.SchedResult{Before: "every 1h", After: c.edit.Spec().Describe(), Reloaded: true, Verified: "ok"}, nil
}

func (f *fakeBE) SetSchedule(ctx context.Context, r routine.Routine, e schedule.Edit, o routine.SchedOpts) (routine.SchedResult, error) {
	f.mu.Lock()
	f.schedCtx = ctx
	f.mu.Unlock()
	return f.sched(schedCall{"set", r.Label, e, o})
}

func (f *fakeBE) RevertSchedule(_ context.Context, r routine.Routine, o routine.SchedOpts) (routine.SchedResult, error) {
	return f.sched(schedCall{"revert", r.Label, schedule.Edit{}, o})
}

func (f *fakeBE) Track(l string) (bool, error) {
	f.tracks = append(f.tracks, l)
	if f.trackIn != nil {
		f.trackIn <- struct{}{}
		<-f.trackGate
	}
	return true, f.trackErr
}
func (f *fakeBE) Untrack(l string) (bool, error) {
	f.untracks = append(f.untracks, l)
	return true, f.trackErr
}

func (f *fakeBE) Logs(r routine.Routine, n int) (string, error) {
	f.logsFor = append(f.logsFor, r.Label)
	return f.logsText, f.logsErr
}

// ---- helpers

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func code(n int) *int { return &n }

func rt(label string, ai bool, st *launchd.Status) routine.Routine {
	next := pinned.Add(48 * time.Hour)
	return routine.Routine{Label: label, Source: routine.SourceUser, Path: "/p/" + label + ".plist", Schedule: "daily 09:00",
		Command: []string{"/bin/sh", "-c", "echo hi"}, AI: ai, Status: st, NextRun: &next,
		StdoutPath: "/tmp/" + label + ".out"}
}

func loadedOK() *launchd.Status {
	return &launchd.Status{Loaded: true, State: "not running", Runs: 3, LastExitCode: code(0)}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drain runs the commands a key produced, feeds their messages back into Update, and goes on
// until nothing is left. Ticks use a 1ms interval in tests and stop re-arming once the work is done.
func drain(m *Model, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
			panic("drain: too many steps (a tick that never stops?)")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, []tea.Cmd(msg)...)
		default:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		_, cmd := m.Update(key(k))
		drain(m, cmd)
	}
}

// typeText types into the focused input. The commands textinput returns are cursor-blink timers
// (about half a second each), which a test must not wait for, so they are not driven.
func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(key(string(r)))
	}
}

func newModel(be Backend) *Model {
	m := New(context.Background(), be)
	m.tickEvery = time.Millisecond
	m.now = func() time.Time { return pinned }
	drain(m, m.Init())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func view(m *Model) string { return plain(m.View()) }

func sample() *fakeBE {
	return &fakeBE{rs: []routine.Routine{
		rt("alpha.ai", true, loadedOK()),
		rt("beta.plain", false, loadedOK()),
		rt("gamma.ai", true, &launchd.Status{Loaded: true, State: "running", PID: 42, Runs: 1}),
		rt("delta.ai", true, &launchd.Status{}),
	}}
}

// ---- tests

func TestListShowsAIOnlyAndToggles(t *testing.T) {
	m := newModel(sample())
	v := view(m)
	for _, want := range []string{"alpha.ai", "gamma.ai", "delta.ai", "AI만 · ", "LABEL", "다음 실행", "실행 중", "미로드", "종료 0", "enter 상세", "q 종료"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in\n%s", want, v)
		}
	}
	if strings.Contains(v, "beta.plain") {
		t.Errorf("a non-AI routine must be hidden by default:\n%s", v)
	}
	press(m, "a")
	if v := view(m); !strings.Contains(v, "beta.plain") || !strings.Contains(v, "전체 · ") {
		t.Errorf("a must show everything:\n%s", v)
	}
	press(m, "a")
	if v := view(m); strings.Contains(v, "beta.plain") || !strings.Contains(v, "AI만 · ") {
		t.Errorf("a must toggle back:\n%s", v)
	}
}

func TestCursorDetailAndBack(t *testing.T) {
	m := newModel(sample())
	if r, _ := m.selected(); r.Label != "alpha.ai" {
		t.Fatalf("first row = %s", r.Label)
	}
	press(m, "j", "j")
	if r, _ := m.selected(); r.Label != "delta.ai" {
		t.Fatalf("after j j: %s", r.Label)
	}
	press(m, "j") // already at the bottom
	if r, _ := m.selected(); r.Label != "delta.ai" {
		t.Fatalf("must stop at the last row: %s", r.Label)
	}
	press(m, "k", "up")
	if r, _ := m.selected(); r.Label != "alpha.ai" {
		t.Fatalf("after k up: %s", r.Label)
	}

	press(m, "down", "enter")
	if m.screen != scDetail {
		t.Fatal("enter must open the detail screen")
	}
	v := view(m)
	for _, want := range []string{"상세", "gamma.ai", "라벨", "일정", "daily 09:00", "상태", "running (pid 42)", "명령", "/bin/sh -c echo hi", "esc 목록"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail missing %q in\n%s", want, v)
		}
	}
	press(m, "esc")
	if m.screen != scList {
		t.Fatal("esc must go back to the list")
	}
	if r, _ := m.selected(); r.Label != "gamma.ai" {
		t.Errorf("the cursor must stay where it was: %s", r.Label)
	}
}

func TestSearch(t *testing.T) {
	m := newModel(sample())
	press(m, "a", "/")
	if !m.searching {
		t.Fatal("/ must start a search")
	}
	typeText(m, "gam")
	if v := view(m); !strings.Contains(v, "gamma.ai") || strings.Contains(v, "alpha.ai") || !strings.Contains(v, "/ gam") {
		t.Errorf("filter not applied:\n%s", v)
	}
	press(m, "enter")
	if m.searching || m.filter != "gam" {
		t.Errorf("enter keeps the filter: searching=%v filter=%q", m.searching, m.filter)
	}
	if v := view(m); !strings.Contains(v, "필터: gam") {
		t.Errorf("the active filter must be shown:\n%s", v)
	}
	// a second search starts from nothing instead of being appended to the first one
	press(m, "/")
	if m.filter != "" || m.search.Value() != "" {
		t.Errorf("/ must start a fresh search: filter=%q box=%q", m.filter, m.search.Value())
	}
	typeText(m, "alp")
	if v := view(m); !strings.Contains(v, "alpha.ai") || strings.Contains(v, "gamma.ai") {
		t.Errorf("second search:\n%s", v)
	}
	press(m, "esc")
	if m.filter != "" || !strings.Contains(view(m), "gamma.ai") {
		t.Errorf("esc must clear the filter: %q", m.filter)
	}
	// keys typed in the search box are text, not commands
	press(m, "/")
	typeText(m, "qrt")
	if m.quit {
		t.Error("q typed into the search box must not quit")
	}
}

func TestTrackToggles(t *testing.T) {
	be := sample()
	be.rs[3].Tracked = true // delta.ai
	m := newModel(be)
	press(m, "j", "t") // gamma.ai
	if len(be.tracks) != 1 || be.tracks[0] != "gamma.ai" || len(be.untracks) != 0 {
		t.Fatalf("tracks=%v untracks=%v", be.tracks, be.untracks)
	}
	if !strings.Contains(view(m), "추적 지정: gamma.ai") {
		t.Errorf("status:\n%s", view(m))
	}
	if be.loads != 2 {
		t.Errorf("the list must be reloaded after tracking (loads=%d)", be.loads)
	}
	if r, _ := m.selected(); r.Label != "gamma.ai" {
		t.Errorf("the cursor must stay on gamma.ai after the reload: %s", r.Label)
	}
	press(m, "j", "t") // delta.ai is tracked: toggles off
	if len(be.untracks) != 1 || be.untracks[0] != "delta.ai" {
		t.Errorf("untracks=%v", be.untracks)
	}
	be.trackErr = errors.New("config.json: broken")
	press(m, "t")
	if v := view(m); !strings.Contains(v, "config.json: broken") {
		t.Errorf("a failing track must be shown:\n%s", v)
	}
}

func TestRunCallsTheBackendAndReportsTheResult(t *testing.T) {
	be := sample()
	m := newModel(be)
	press(m, "r")
	if len(be.runs) != 1 || be.runLabel[0] != "alpha.ai" {
		t.Fatalf("runs=%v labels=%v", be.runs, be.runLabel)
	}
	if o := be.runs[0]; o.Load || o.Restart || o.Yes || o.Wait != 10*time.Second {
		t.Errorf("a plain r must ask for no confirmation flags: %+v", o)
	}
	if !strings.Contains(view(m), "alpha.ai: 종료 코드 0 (runs 1)") {
		t.Errorf("status:\n%s", view(m))
	}
	if be.loads != 2 || m.work != nil {
		t.Errorf("reload after the run: loads=%d work=%v", be.loads, m.work)
	}

	// a failing job
	be.runFn = func(context.Context, routine.Routine, routine.RunOpts) (routine.RunResult, error) {
		return routine.RunResult{Started: true, Finished: true, Status: launchd.Status{Loaded: true, Runs: 2, LastExitCode: code(1)}}, nil
	}
	press(m, "r")
	if v := view(m); !strings.Contains(v, "실패, 종료 코드 1") {
		t.Errorf("status:\n%s", v)
	}
	// still running after the wait
	be.runFn = func(context.Context, routine.Routine, routine.RunOpts) (routine.RunResult, error) {
		return routine.RunResult{Started: true, Status: launchd.Status{Loaded: true, State: "running", PID: 7}}, nil
	}
	press(m, "r")
	if v := view(m); !strings.Contains(v, "10초 안에 끝나지 않았습니다") || !strings.Contains(v, "pid 7") {
		t.Errorf("status:\n%s", v)
	}
	// a hard error
	be.runFn = func(context.Context, routine.Routine, routine.RunOpts) (routine.RunResult, error) {
		return routine.RunResult{}, errors.New("launchctl: boom")
	}
	press(m, "r")
	if v := view(m); !strings.Contains(v, "launchctl: boom") {
		t.Errorf("status:\n%s", v)
	}
}

// The TUI has no policy: whatever the policy function asks for is a dialog, and the answer goes
// back as the flags the policy function understands.
func TestRunConfirmationDialogs(t *testing.T) {
	for _, c := range []struct {
		need    routine.Need
		text    string
		granted func(routine.RunOpts) bool
	}{
		{routine.NeedLoad, "등록(bootstrap)", func(o routine.RunOpts) bool { return o.Load && !o.Restart && !o.Yes }},
		{routine.NeedRestart, "재시작하면 진행 중인 작업이 중단됩니다", func(o routine.RunOpts) bool { return o.Restart && !o.Yes && !o.Load }},
		{routine.NeedRestartKeepAlive, "KeepAlive 상시 서비스", func(o routine.RunOpts) bool { return o.Restart && o.Yes }},
	} {
		be := sample()
		need := c.need
		be.runFn = func(_ context.Context, _ routine.Routine, o routine.RunOpts) (routine.RunResult, error) {
			probe := routine.RunOpts{Load: o.Load, Restart: o.Restart, Yes: o.Yes}
			if !c.granted(probe) {
				return routine.RunResult{}, &routine.ConfirmError{Need: need, Msg: "needs a yes"}
			}
			zero := 0
			return routine.RunResult{Started: true, Finished: true, Status: launchd.Status{Loaded: true, Runs: 1, LastExitCode: &zero}}, nil
		}
		m := newModel(be)

		press(m, "r")
		if m.dlg == nil || !strings.Contains(view(m), c.text) || !strings.Contains(view(m), "[y] 예") {
			t.Fatalf("%s: no dialog:\n%s", c.need, view(m))
		}
		if len(be.runs) != 1 {
			t.Fatalf("%s: runs=%d before the answer", c.need, len(be.runs))
		}
		press(m, "n") // no: nothing more happens
		if m.dlg != nil || len(be.runs) != 1 || !strings.Contains(view(m), "취소했습니다") {
			t.Fatalf("%s: n must cancel (runs=%d)", c.need, len(be.runs))
		}
		press(m, "x") // not a command while... (x is unbound) and no dialog is open
		press(m, "r", "y")
		if len(be.runs) != 3 || !c.granted(be.runs[2]) {
			t.Fatalf("%s: runs=%+v, want the third call to carry the grant", c.need, be.runs)
		}
		if !strings.Contains(view(m), "종료 코드 0") {
			t.Errorf("%s: after yes the run must complete:\n%s", c.need, view(m))
		}
	}
}

func TestDialogSwallowsOtherKeys(t *testing.T) {
	be := sample()
	be.runFn = func(context.Context, routine.Routine, routine.RunOpts) (routine.RunResult, error) {
		return routine.RunResult{}, &routine.ConfirmError{Need: routine.NeedRestart, Msg: "x"}
	}
	m := newModel(be)
	press(m, "r")
	press(m, "q", "j", "a", "e")
	if m.quit || m.cursor != 0 || m.showAll || m.dlg == nil {
		t.Errorf("only y/n/esc work in a dialog: quit=%v cursor=%d showAll=%v dlg=%v", m.quit, m.cursor, m.showAll, m.dlg != nil)
	}
	press(m, "esc")
	if m.dlg != nil {
		t.Error("esc closes the dialog")
	}
}

func TestScheduleForm(t *testing.T) {
	be := sample()
	m := newModel(be)
	press(m, "e")
	if m.screen != scForm {
		t.Fatal("e must open the form")
	}
	v := view(m)
	for _, want := range []string{"일정 변경", "alpha.ai", "현재 일정", "daily 09:00", "weekly mon,wed 09:00", "revert", "enter 적용"} {
		if !strings.Contains(v, want) {
			t.Errorf("form missing %q in\n%s", want, v)
		}
	}

	// a typo stays in the form and calls nothing
	typeText(m, "daily 25:00")
	press(m, "enter")
	if m.screen != scForm || len(be.scheds) != 0 || !strings.Contains(view(m), "bad time") {
		t.Fatalf("screen=%v calls=%d\n%s", m.screen, len(be.scheds), view(m))
	}
	// fix it
	m.form.SetValue("")
	typeText(m, "weekly mon,wed 09:00")
	press(m, "enter")
	if m.screen != scList || len(be.scheds) != 1 {
		t.Fatalf("screen=%v calls=%d", m.screen, len(be.scheds))
	}
	c := be.scheds[0]
	if c.kind != "set" || c.r != "alpha.ai" || c.edit.Spec().Describe() != "weekly mon 09:00, weekly wed 09:00" || c.opts != (routine.SchedOpts{}) {
		t.Errorf("call = %+v", c)
	}
	if v := view(m); !strings.Contains(v, "alpha.ai: every 1h → weekly mon 09:00, weekly wed 09:00 (다시 로드함)") {
		t.Errorf("status:\n%s", v)
	}

	// esc leaves the form without a call
	press(m, "e", "esc")
	if m.screen != scList || len(be.scheds) != 1 {
		t.Errorf("esc: screen=%v calls=%d", m.screen, len(be.scheds))
	}

	// revert
	press(m, "e")
	typeText(m, "revert")
	press(m, "enter")
	if len(be.scheds) != 2 || be.scheds[1].kind != "revert" {
		t.Errorf("calls = %+v", be.scheds)
	}

	// from the detail screen the form returns there
	press(m, "enter", "e", "esc")
	if m.screen != scDetail {
		t.Errorf("the form must return to where it was opened: %v", m.screen)
	}
}

func TestScheduleConfirmationDialogs(t *testing.T) {
	for _, c := range []struct {
		need routine.Need
		text string
		ok   func(routine.SchedOpts) bool
	}{
		{routine.NeedReloadRunning, "실행 중인 작업이 중단됩니다", func(o routine.SchedOpts) bool { return o.Yes && !o.Force }},
		{routine.NeedForce, "표현할 수 없는 형식", func(o routine.SchedOpts) bool { return o.Force && !o.Yes }},
	} {
		be := sample()
		need, ok := c.need, c.ok
		be.schedFn = func(sc schedCall) (routine.SchedResult, error) {
			if !ok(sc.opts) {
				return routine.SchedResult{}, &routine.ConfirmError{Need: need, Msg: "x"}
			}
			return routine.SchedResult{Before: "a", After: "b"}, nil
		}
		m := newModel(be)
		press(m, "e")
		typeText(m, "daily 05:00")
		press(m, "enter")
		if m.dlg == nil || !strings.Contains(view(m), c.text) {
			t.Fatalf("%s: no dialog:\n%s", c.need, view(m))
		}
		press(m, "n")
		if len(be.scheds) != 1 {
			t.Fatalf("%s: n must not retry", c.need)
		}
		press(m, "e")
		typeText(m, "daily 05:00")
		press(m, "enter", "y")
		if len(be.scheds) != 3 || !ok(be.scheds[2].opts) || be.scheds[2].edit.Spec().Describe() != "daily 05:00" {
			t.Fatalf("%s: calls = %+v", c.need, be.scheds)
		}
		if !strings.Contains(view(m), "alpha.ai: a → b") {
			t.Errorf("%s: status:\n%s", c.need, view(m))
		}
	}
}

func TestScheduleRefusedBeforeTheForm(t *testing.T) {
	be := sample()
	be.canEdit = errors.New("alpha.ai is in /Library/LaunchAgents, which rtm only reads")
	m := newModel(be)
	press(m, "e")
	if m.screen != scList || !strings.Contains(view(m), "which rtm only reads") {
		t.Errorf("screen=%v\n%s", m.screen, view(m))
	}
}

func TestLogs(t *testing.T) {
	be := sample()
	be.logsText = "first line\nsecond line\nlast line"
	m := newModel(be)
	press(m, "l")
	if m.screen != scLogs || len(be.logsFor) != 1 || be.logsFor[0] != "alpha.ai" {
		t.Fatalf("screen=%v logsFor=%v", m.screen, be.logsFor)
	}
	v := view(m)
	for _, want := range []string{"로그", "last line", "esc 뒤로", "f 따라가기 꺼짐"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in\n%s", want, v)
		}
	}
	_, cmd := m.Update(key("f"))
	if !m.follow || cmd == nil || !strings.Contains(view(m), "f 따라가기 켜짐") {
		t.Errorf("f must turn following on (follow=%v cmd=%v)", m.follow, cmd != nil)
	}
	// a follow tick fetches again
	be.logsText += "\nnewest line"
	_, tickCmd := m.Update(followMsg{m.followGen}) // one follow tick: [fetch, next tick]; the tick re-arms by design
	batch, ok := tickCmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a follow tick must fetch and re-arm: %T", tickCmd())
	}
	_, after := m.Update(batch[0]())
	drain(m, after)
	if !strings.Contains(view(m), "newest line") || len(be.logsFor) < 2 {
		t.Errorf("follow must refetch:\n%s", view(m))
	}
	press(m, "f")
	if m.follow {
		t.Error("f again turns it off")
	}
	press(m, "esc")
	if m.screen != scList {
		t.Errorf("esc must return to the list it was opened from: %v", m.screen)
	}
	press(m, "enter", "l", "esc")
	if m.screen != scDetail {
		t.Errorf("opened from the detail screen, esc returns there: %v", m.screen)
	}
	press(m, "esc")

	// no log path: say so, stay on the list
	be.logsErr = errors.New("alpha.ai has no log path")
	press(m, "l")
	if m.screen != scList || !strings.Contains(view(m), "has no log path") {
		t.Errorf("screen=%v\n%s", m.screen, view(m))
	}
}

func TestProgressBarAndEscCancelsTheWait(t *testing.T) {
	m := newModel(sample())
	cancelled := false
	m.work = &working{kind: "run", label: "alpha.ai", started: time.Now().Add(-3 * time.Second), cancel: func() { cancelled = true }}
	v := view(m)
	if !strings.Contains(v, "alpha.ai 실행 중… [■■■□□□□□□□] 3/10초") {
		t.Errorf("bar at 3s:\n%s", v)
	}
	m.work.started = time.Now().Add(-30 * time.Second)
	if !strings.Contains(view(m), "[■■■■■■■■■■] 10/10초") {
		t.Errorf("the bar must stop at full:\n%s", view(m))
	}
	m.work.started = time.Now()
	if !strings.Contains(view(m), "[□□□□□□□□□□] 0/10초") {
		t.Errorf("bar at 0:\n%s", view(m))
	}
	if !strings.Contains(view(m), "esc 대기 중단") {
		t.Errorf("help while waiting:\n%s", view(m))
	}
	// while waiting, other keys do nothing
	press(m, "q", "j", "a", "r")
	if m.quit || m.cursor != 0 || m.showAll || cancelled {
		t.Errorf("only esc and ctrl+c work while waiting (quit=%v cancelled=%v)", m.quit, cancelled)
	}
	press(m, "esc")
	if !cancelled {
		t.Error("esc must cancel the wait")
	}
}

// End to end with a Run that blocks until its context is cancelled: esc reaches the backend.
func TestEscCancelsTheBackendRun(t *testing.T) {
	be := sample()
	started := make(chan struct{})
	be.runFn = func(ctx context.Context, _ routine.Routine, _ routine.RunOpts) (routine.RunResult, error) {
		close(started)
		<-ctx.Done()
		return routine.RunResult{Started: true}, ctx.Err()
	}
	m := newModel(be)
	_, cmd := m.Update(key("r"))
	batch := cmd().(tea.BatchMsg) // [run, tick]
	done := make(chan tea.Msg, 1)
	go func() { done <- batch[0]() }()
	<-started
	if m.work == nil {
		t.Fatal("the run must be in flight")
	}
	press(m, "esc")
	select {
	case msg := <-done:
		_, next := m.Update(msg)
		drain(m, next)
	case <-time.After(3 * time.Second):
		t.Fatal("esc did not cancel the backend call")
	}
	if m.work != nil || !strings.Contains(view(m), "대기를 중단했습니다. 이미 시작한 작업은 계속 실행됩니다") {
		t.Errorf("status:\n%s", view(m))
	}
}

func TestQuit(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m := newModel(sample())
		_, cmd := m.Update(key(k))
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok || !m.quit {
			t.Errorf("%s must quit", k)
		}
	}
	m := newModel(sample())
	press(m, "enter")
	if _, cmd := m.Update(key("q")); cmd == nil {
		t.Error("q quits from the detail screen too")
	}
}

func TestScanWarningsAreMentioned(t *testing.T) {
	be := sample()
	be.warns = []string{"a: no Label", "b: no Label"}
	if v := view(newModel(be)); !strings.Contains(v, "스캔 경고 2건") {
		t.Errorf("view:\n%s", v)
	}
}

func TestEmptyAndLoadingScreens(t *testing.T) {
	m := New(context.Background(), &fakeBE{})
	if v := plain(m.View()); !strings.Contains(v, "불러오는 중") {
		t.Errorf("loading:\n%s", v)
	}
	m2 := newModel(&fakeBE{})
	if v := view(m2); !strings.Contains(v, "표시할 루틴이 없습니다") || !strings.Contains(v, "a 를 눌러") {
		t.Errorf("empty:\n%s", v)
	}
	press(m2, "enter", "r", "e", "l", "t", "j", "k") // nothing selected: nothing may crash or be called
	if m2.screen != scList {
		t.Errorf("screen = %v", m2.screen)
	}
}

// Hangul takes two columns. The table must line up by display width, not by rune count.
func TestTableAlignsWithHangul(t *testing.T) {
	be := &fakeBE{rs: []routine.Routine{
		rt("abc", true, loadedOK()),
		rt("한글라벨-일정관리", true, &launchd.Status{Loaded: true, State: "running", PID: 1}),
		rt("가나다라마바사아자차카타파하거너더러머버서어저처커터퍼허", true, &launchd.Status{}), // too long: must be cut
		rt("mixed-한글-abc", true, &launchd.Status{Loaded: true, Runs: 1, LastExitCode: code(2)}),
	}}
	for _, w := range []int{100, 80, 60, 40} {
		m := newModel(be)
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		lines := strings.Split(view(m), "\n")
		if len(lines) != 24 {
			t.Errorf("w=%d: %d lines, want exactly 24 (status and help stay at the bottom)", w, len(lines))
		}
		stateCol := -1
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("w=%d: line is %d columns wide: %q", w, lipgloss.Width(l), l)
			}
		}
		if w < 80 {
			continue
		}
		for _, l := range lines {
			for _, st := range []string{"종료 0", "실행 중", "미로드", "실패 2"} {
				if i := strings.Index(l, st); i >= 0 {
					col := lipgloss.Width(l[:i])
					if stateCol == -1 {
						stateCol = col
					}
					if col != stateCol {
						t.Errorf("w=%d: the 상태 column starts at %d here but %d elsewhere: %q", w, col, stateCol, l)
					}
				}
			}
		}
		if stateCol == -1 {
			t.Errorf("w=%d: no state cells found", w)
		}
	}
}

func TestEstimatedNextRunIsLabelled(t *testing.T) {
	be := sample()
	be.rs[0].NextRunEstimated = true
	if v := view(newModel(be)); !strings.Contains(v, "(estimated)") {
		t.Errorf("an estimate must say so:\n%s", v)
	}
}

func TestTinyTerminalDoesNotCrash(t *testing.T) {
	m := newModel(sample())
	for _, size := range [][2]int{{1, 1}, {10, 3}, {20, 6}, {200, 80}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, k := range []string{"j", "enter", "esc", "e", "esc", "l", "esc", "a", "/", "esc"} {
			press(m, k)
			_ = m.View()
		}
	}
	_ = fmt.Sprint(m.View())
}

// A list longer than the screen scrolls so that the cursor row is always on screen.
func TestListScrollsToKeepTheCursorVisible(t *testing.T) {
	be := &fakeBE{}
	for i := 0; i < 40; i++ {
		be.rs = append(be.rs, rt(fmt.Sprintf("job-%02d", i), true, loadedOK()))
	}
	m := newModel(be)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 10}) // 5 rows fit
	if v := view(m); !strings.Contains(v, "job-00") || strings.Contains(v, "job-06") {
		t.Fatalf("start:\n%s", v)
	}
	for i := 1; i <= 30; i++ {
		press(m, "j")
		want := fmt.Sprintf("job-%02d", i)
		if v := view(m); !strings.Contains(v, want) {
			t.Fatalf("after %d presses of j the cursor row %s is not on screen:\n%s", i, want, v)
		}
		if lines := strings.Split(view(m), "\n"); len(lines) != 10 {
			t.Fatalf("the screen must stay exactly 10 lines, got %d", len(lines))
		}
	}
	for i := 30; i > 12; i-- {
		press(m, "k")
		if want := fmt.Sprintf("job-%02d", i-1); !strings.Contains(view(m), want) {
			t.Fatalf("going up: %s not on screen:\n%s", want, view(m))
		}
	}
	press(m, "pgdown", "pgdown", "pgdown", "pgdown", "pgdown", "pgdown")
	if r, _ := m.selected(); r.Label != "job-39" || !strings.Contains(view(m), "job-39") {
		t.Errorf("pgdown must stop at the last row: %s", r.Label)
	}
	press(m, "pgup")
	if r, _ := m.selected(); r.Label != "job-34" {
		t.Errorf("pgup moves a screen: %s", r.Label)
	}
}

// ---- M5 review fixes

// P0: config.json is read-modify-written, so a second t while one is in flight must not start.
func TestTrackIsSerialized(t *testing.T) {
	be := sample()
	be.trackIn, be.trackGate = make(chan struct{}, 4), make(chan struct{})
	m := newModel(be)
	_, cmd := m.Update(key("t")) // alpha.ai
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-be.trackIn

	_, c2 := m.Update(key("t")) // pressed again while the first is in flight
	if c2 != nil {
		t.Error("a second t must not start a backend call")
	}
	if !strings.Contains(view(m), "추적 변경이 아직 끝나지 않았습니다") {
		t.Errorf("the user must be told:\n%s", view(m))
	}
	close(be.trackGate)
	msg := <-done
	if len(be.tracks) != 1 {
		t.Fatalf("tracks = %v", be.tracks)
	}
	_, next := m.Update(msg)
	if r, _ := m.selected(); !r.Tracked {
		t.Error("the row must show the new state at once, before the reload")
	}
	be.rs[0].Tracked = true // what the reload will find
	drain(m, next)
	// after it ended, t works again, and now untracks (the row is tracked)
	be.trackIn = nil
	press(m, "t")
	if len(be.untracks) != 1 {
		t.Errorf("tracks=%v untracks=%v", be.tracks, be.untracks)
	}
}

// P1: ctrl+c while a schedule change runs must not quit until the backend returns.
func TestCtrlCDuringAScheduleChangeWaitsForTheBackend(t *testing.T) {
	be := sample()
	release, started := make(chan struct{}), make(chan struct{})
	be.schedFn = func(schedCall) (routine.SchedResult, error) {
		close(started)
		<-release
		return routine.SchedResult{Before: "a", After: "b"}, nil
	}
	m := newModel(be)
	press(m, "e")
	typeText(m, "daily 05:15")
	_, cmd := m.Update(key("enter"))
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started

	_, quit := m.Update(key("ctrl+c"))
	if quit != nil || m.quit {
		t.Fatal("ctrl+c must not quit while the schedule change runs")
	}
	if !strings.Contains(view(m), "변경을 마무리하는 중… 끝나면 종료합니다") {
		t.Errorf("status:\n%s", view(m))
	}
	be.mu.Lock()
	ctx := be.schedCtx
	be.mu.Unlock()
	if ctx.Err() != nil {
		t.Error("the backend's context must not be cancelled by ctrl+c")
	}
	press(m, "q", "esc") // other keys still do nothing special
	if m.quit {
		t.Fatal("q must not quit either")
	}

	close(release)
	_, after := m.Update(<-done)
	if after == nil {
		t.Fatal("the model must quit once the backend returned")
	}
	if _, ok := after().(tea.QuitMsg); !ok || !m.quit {
		t.Error("expected a quit")
	}
}

// A confirmation (nothing changed) or a failure also ends the wait.
func TestCtrlCDuringAScheduleChangeQuitsAfterAFailureToo(t *testing.T) {
	m := newModel(sample())
	m.work = &working{kind: "sched", label: "alpha.ai", cancel: func() {}}
	m.Update(key("ctrl+c"))
	_, cmd := m.Update(schedDoneMsg{p: pending{kind: "sched", r: rt("alpha.ai", true, nil)}, err: errors.New("boom: NOT loaded")})
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected quit")
	}
	// the error must survive the quit: Run hands it to the caller, which prints it and exits 1
	err := result(m, nil)
	if err == nil || !strings.Contains(err.Error(), "alpha.ai") || !strings.Contains(err.Error(), "boom: NOT loaded") {
		t.Errorf("exit error = %v", err)
	}
	if !errors.Is(err, m.exitErr) {
		t.Error("the error must be wrapped, not replaced")
	}
}

// A change that succeeded, or that only asked for a confirmation, leaves nothing to report.
func TestQuitAfterAScheduleChangeThatSucceededExitsClean(t *testing.T) {
	for name, msg := range map[string]schedDoneMsg{
		"success":      {p: pending{kind: "sched"}, res: routine.SchedResult{Before: "a", After: "b"}},
		"confirmation": {p: pending{kind: "sched"}, err: &routine.ConfirmError{Need: routine.NeedReloadRunning}},
	} {
		m := newModel(sample())
		m.work = &working{kind: "sched", label: "alpha.ai", cancel: func() {}}
		m.Update(key("ctrl+c"))
		_, cmd := m.Update(msg)
		if _, ok := cmd().(tea.QuitMsg); !ok || result(m, nil) != nil {
			t.Errorf("%s: quit=%v err=%v", name, cmd != nil, result(m, nil))
		}
	}
}

func TestResultPrefersTheProgramsOwnError(t *testing.T) {
	m := newModel(sample())
	m.exitErr = errors.New("kept")
	own := errors.New("program failed")
	if err := result(m, own); err != own {
		t.Errorf("err = %v", err)
	}
}

// A wait for a run may still be abandoned at once: the job keeps running under launchd.
func TestCtrlCDuringARunCancelsAndQuitsAtOnce(t *testing.T) {
	m := newModel(sample())
	cancelled := false
	m.work = &working{kind: "run", label: "alpha.ai", cancel: func() { cancelled = true }}
	_, cmd := m.Update(key("ctrl+c"))
	if _, ok := cmd().(tea.QuitMsg); !ok || !cancelled || !m.quit {
		t.Errorf("quit=%v cancelled=%v", m.quit, cancelled)
	}
}

// P1: a log result that arrives late must not reopen a screen the user left, or replace another
// routine's log.
func TestLateLogsAreIgnored(t *testing.T) {
	be := sample()
	m := newModel(be)

	// two requests: alpha then beta; beta's result arrives first, then alpha's
	be.logsText = "alpha text"
	_, ca := m.Update(key("l"))
	press(m, "esc") // nothing to leave yet, still the list
	press(m, "j")   // gamma.ai
	be.logsText = "beta text"
	_, cb := m.Update(key("l"))
	m.Update(cb())
	if m.screen != scLogs || m.logsFor.Label != "gamma.ai" {
		t.Fatalf("screen=%v logsFor=%s", m.screen, m.logsFor.Label)
	}
	be.logsText = "alpha text"
	m.Update(ca())
	if m.logsFor.Label != "gamma.ai" || strings.Contains(view(m), "alpha text") {
		t.Errorf("the old request overwrote the log:\n%s", view(m))
	}

	// leaving: a result in flight when the user pressed esc must not reopen the view
	be.logsText = "x"
	m.Update(key("f")) // not press: following never ends
	_, fetch := m.Update(followMsg{m.followGen})
	msgs := fetch().(tea.BatchMsg)
	late := msgs[0]() // a fetch whose answer is still on its way
	press(m, "esc")
	if m.screen != scList {
		t.Fatal("esc must leave")
	}
	m.Update(late)
	if m.screen != scList {
		t.Errorf("a late log result reopened the log screen: %v", m.screen)
	}

	// the user moved on to the form before the answer came
	_, cl := m.Update(key("l"))
	late = cl()
	press(m, "e")
	m.Update(late)
	if m.screen != scForm {
		t.Errorf("a late log result took over the form: %v", m.screen)
	}
}

// P1: switching follow off and on must leave one tick chain, not two.
func TestFollowToggleDoesNotMultiplyTickChains(t *testing.T) {
	be := sample()
	be.logsText = "line"
	m := newModel(be)
	press(m, "l")
	_, c1 := m.Update(key("f")) // on: chain 1
	m.Update(key("f"))          // off
	_, c3 := m.Update(key("f")) // on: chain 3
	old, cur := c1(), c3()
	if _, cmd := m.Update(old); cmd != nil {
		t.Error("the tick of the old chain must die")
	}
	if _, cmd := m.Update(cur); cmd == nil {
		t.Error("the current chain must go on")
	}
}
