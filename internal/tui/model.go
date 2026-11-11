// Package tui is the terminal UI: screens and input handling only. What happens when a key is
// pressed is a call into internal/routine through Backend; the TUI has no policy of its own.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

type screen int

const (
	scList screen = iota
	scDetail
	scLogs
	scForm
)

const logLines = 300

// progressCells is the width of the wait bar: one cell per second of the default 10s wait.
const progressCells = 10

type pending struct {
	kind   string // "run" | "sched" | "revert"
	r      routine.Routine
	run    routine.RunOpts
	sched  routine.SchedOpts
	edit   schedule.Edit
	reason *routine.ConfirmError
}

// working is the action in flight: a run (with the wait bar) or a schedule change.
type working struct {
	kind    string // "run" | "sched"
	label   string
	started time.Time
	cancel  context.CancelFunc
}

type Model struct {
	be  Backend
	ctx context.Context

	width, height int
	tickEvery     time.Duration
	wait          time.Duration    // how long a run waits for the job to finish (the bar's length)
	now           func() time.Time // tests pin it

	all     []routine.Routine
	warns   []string
	loading bool
	vis     []int // indexes into all that pass the filters
	cursor  int
	top     int
	keep    string // label to put the cursor back on after a reload
	showAll bool   // false: AI routines only
	filter  string

	screen   screen
	prev     screen // where the schedule form was opened from
	logsFrom screen // where the log view was opened from
	status   string
	statusIs bool // true: error

	searching bool
	search    textinput.Model

	form    textinput.Model
	formErr string
	formFor routine.Routine

	vp      viewport.Model
	logsFor routine.Routine
	follow  bool
	// Generations: a late message from an old request must not act on the current screen.
	logsGen   int  // bumped when a log view is requested or left
	followGen int  // bumped when following is switched on or off, so an old tick chain dies
	tracking  bool // a track/untrack is in flight: config.json is read-modify-written, one at a time

	dlg  *pending
	work *working
	quit bool
	// quitAfter: ctrl+c arrived while a schedule change was in flight. Quitting now could leave the
	// job booted out; rtm quits when the backend (restore included) has finished.
	quitAfter bool
	// exitErr is what the backend reported while the quit was held back (NOT loaded, a failed
	// restore, the backup path ...). The screen is gone by then, so Run hands it to the caller.
	exitErr error
}

func New(ctx context.Context, be Backend) *Model {
	search := textinput.New()
	search.Prompt = "/ "
	search.CharLimit = 80
	form := textinput.New()
	form.Prompt = "> "
	form.CharLimit = 120
	form.Placeholder = "daily 09:00"
	return &Model{
		be: be, ctx: ctx, loading: true, search: search, form: form,
		tickEvery: time.Second, wait: 10 * time.Second, now: time.Now,
		width: 100, height: 30, vp: viewport.New(100, 20),
	}
}

// ---- messages

type loadedMsg struct {
	rs    []routine.Routine
	warns []string
}
type runDoneMsg struct {
	r    routine.Routine
	opts routine.RunOpts
	res  routine.RunResult
	err  error
}
type schedDoneMsg struct {
	p   pending
	res routine.SchedResult
	err error
}
type logsMsg struct {
	gen  int
	r    routine.Routine
	text string
	err  error
}
type trackMsg struct {
	label   string
	tracked bool // the new state
	changed bool
	err     error
}
type tickMsg struct{}
type followMsg struct{ gen int }

// ---- commands

func (m *Model) load() tea.Cmd {
	return func() tea.Msg {
		rs, w := m.be.Load()
		return loadedMsg{rs, w}
	}
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) Init() tea.Cmd { return m.load() }

func (m *Model) startRun(r routine.Routine, o routine.RunOpts) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.work = &working{kind: "run", label: r.Label, started: time.Now(), cancel: cancel}
	o.Wait = m.wait
	m.status = ""
	return tea.Batch(func() tea.Msg {
		res, err := m.be.Run(ctx, r, o)
		return runDoneMsg{r, o, res, err}
	}, m.tick())
}

func (m *Model) startSched(p pending) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.work = &working{kind: "sched", label: p.r.Label, started: time.Now(), cancel: cancel}
	m.status = ""
	return func() tea.Msg {
		var res routine.SchedResult
		var err error
		if p.kind == "revert" {
			res, err = m.be.RevertSchedule(ctx, p.r, p.sched)
		} else {
			res, err = m.be.SetSchedule(ctx, p.r, p.edit, p.sched)
		}
		return schedDoneMsg{p, res, err}
	}
}

func (m *Model) fetchLogs(r routine.Routine) tea.Cmd {
	gen := m.logsGen
	return func() tea.Msg {
		text, err := m.be.Logs(r, logLines)
		return logsMsg{gen, r, text, err}
	}
}

func (m *Model) followTick() tea.Cmd {
	gen := m.followGen
	return tea.Tick(m.followEvery(), func(time.Time) tea.Msg { return followMsg{gen} })
}

// leaveLogs closes the log view and invalidates whatever is still in flight for it.
func (m *Model) leaveLogs() {
	m.follow = false
	m.followGen++
	m.logsGen++
	m.screen = m.logsFrom
}

// ---- state helpers

func (m *Model) refilter() {
	m.vis = m.vis[:0]
	q := strings.ToLower(m.filter)
	for i, r := range m.all {
		if !m.showAll && !r.AI {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Label+" "+r.Schedule), q) {
			continue
		}
		m.vis = append(m.vis, i)
	}
	m.cursor = min(m.cursor, max(0, len(m.vis)-1))
	m.fixScroll()
}

func (m *Model) selected() (routine.Routine, bool) {
	if m.cursor < 0 || m.cursor >= len(m.vis) {
		return routine.Routine{}, false
	}
	return m.all[m.vis[m.cursor]], true
}

func (m *Model) rowsVisible() int { return max(1, m.height-5) } // header, column titles, blank, status, help

func (m *Model) fixScroll() {
	rows := m.rowsVisible()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+rows {
		m.top = m.cursor - rows + 1
	}
	m.top = max(0, m.top)
}

func (m *Model) setStatus(s string, isErr bool) { m.status, m.statusIs = s, isErr }

func (m *Model) cancelWork() {
	if m.work != nil {
		m.work.cancel()
	}
}

// ---- Update

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.vp.Width, m.vp.Height = msg.Width, max(1, msg.Height-3)
		m.form.Width = max(10, msg.Width-10)
		m.fixScroll()
		return m, nil

	case loadedMsg:
		m.loading = false
		m.all, m.warns = msg.rs, msg.warns
		if m.keep != "" {
			// re-find the row the cursor was on
			m.cursor = 0
			m.refilter()
			for i, idx := range m.vis {
				if m.all[idx].Label == m.keep {
					m.cursor = i
				}
			}
			m.keep = ""
		}
		m.refilter()
		m.fixScroll()
		if len(m.warns) > 0 && m.status == "" {
			m.setStatus(fmt.Sprintf("스캔 경고 %d건 (rtm doctor 로 확인)", len(m.warns)), false)
		}
		return m, nil

	case tickMsg:
		if m.work != nil && m.work.kind == "run" {
			return m, m.tick() // redraw the wait bar
		}
		return m, nil

	case runDoneMsg:
		return m.runDone(msg)

	case schedDoneMsg:
		return m.schedDone(msg)

	case logsMsg:
		if msg.gen != m.logsGen {
			return m, nil // an old request: the view was left or another one was asked for
		}
		if m.screen != scLogs && (m.screen != m.logsFrom || m.work != nil || m.dlg != nil) {
			return m, nil // opening the view, but the user has moved on meanwhile
		}
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			m.follow = false
			return m, nil
		}
		atBottom := m.screen != scLogs || m.vp.AtBottom()
		m.logsFor = msg.r
		m.vp.SetContent(msg.text)
		if atBottom {
			m.vp.GotoBottom()
		}
		m.screen = scLogs
		return m, nil

	case followMsg:
		if msg.gen == m.followGen && m.follow && m.screen == scLogs {
			return m, tea.Batch(m.fetchLogs(m.logsFor), m.followTick())
		}
		return m, nil

	case trackMsg:
		m.tracking = false
		if msg.err == nil {
			for i := range m.all { // the reload below is async; until then the row must not look stale
				if m.all[i].Label == msg.label {
					m.all[i].Tracked = msg.tracked
				}
			}
		}
		switch {
		case msg.err != nil:
			m.setStatus(msg.err.Error(), true)
		case msg.tracked:
			m.setStatus("추적 지정: "+msg.label, false)
		default:
			m.setStatus("추적 해제: "+msg.label, false)
		}
		m.keep = msg.label
		return m, m.load()

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) followEvery() time.Duration { return m.tickEvery }

func (m *Model) runDone(msg runDoneMsg) (tea.Model, tea.Cmd) {
	m.cancelWork()
	m.work = nil
	var ce *routine.ConfirmError
	switch {
	case errors.As(msg.err, &ce):
		m.dlg = &pending{kind: "run", r: msg.r, run: msg.opts, reason: ce}
		return m, nil
	case errors.Is(msg.err, context.Canceled):
		if msg.res.Started {
			m.setStatus(msg.r.Label+": 대기를 중단했습니다. 이미 시작한 작업은 계속 실행됩니다", false)
		} else {
			m.setStatus(msg.r.Label+": 중단했습니다. 시작하지 않았습니다", false)
		}
	case msg.err != nil:
		m.setStatus(msg.err.Error(), true)
	case !msg.res.Finished:
		st := msg.res.Status
		m.setStatus(fmt.Sprintf("%s: %s초 안에 끝나지 않았습니다 (상태 %s, pid %d)", msg.r.Label, secs(m.wait), st.State, st.PID), false)
	case msg.res.Status.LastExitCode != nil && *msg.res.Status.LastExitCode != 0:
		m.setStatus(fmt.Sprintf("%s: 실패, 종료 코드 %d (runs %d)", msg.r.Label, *msg.res.Status.LastExitCode, msg.res.Status.Runs), true)
	default:
		code := 0
		if msg.res.Status.LastExitCode != nil {
			code = *msg.res.Status.LastExitCode
		}
		m.setStatus(fmt.Sprintf("%s: 종료 코드 %d (runs %d)", msg.r.Label, code, msg.res.Status.Runs), false)
	}
	m.keep = msg.r.Label
	return m, m.load()
}

func (m *Model) schedDone(msg schedDoneMsg) (tea.Model, tea.Cmd) {
	m.cancelWork()
	m.work = nil
	var ce *routine.ConfirmError
	if m.quitAfter { // the user asked to quit while this ran; the backend has ended, so quit
		// Every error is kept: a ConfirmError can wrap a failure that needs manual recovery
		// (the plist could not be restored), so it is not safe to treat it as "nothing happened".
		if _, bare := msg.err.(*routine.ConfirmError); bare {
			m.exitErr = fmt.Errorf("%s: the schedule change needed a confirmation, so it was not made (rtm was quitting): %w", msg.p.r.Label, msg.err)
		} else if msg.err != nil {
			m.exitErr = fmt.Errorf("%s: the schedule change ended with an error while rtm was quitting: %w", msg.p.r.Label, msg.err)
		}
		m.quit = true
		return m, tea.Quit
	}
	switch {
	case errors.As(msg.err, &ce):
		p := msg.p
		p.reason = ce
		m.dlg = &p
		return m, nil
	case msg.err != nil:
		m.setStatus(msg.err.Error(), true)
	case msg.res.Unchanged:
		m.setStatus(msg.p.r.Label+": 이미 같은 일정입니다 ("+msg.res.After+")", false)
	default:
		s := fmt.Sprintf("%s: %s → %s", msg.p.r.Label, msg.res.Before, msg.res.After)
		if msg.res.Reloaded {
			s += " (다시 로드함)"
		}
		m.setStatus(s, false)
	}
	m.keep = msg.p.r.Label
	return m, m.load()
}

func secs(d time.Duration) string { return fmt.Sprint(int(d / time.Second)) }

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		if m.work != nil && m.work.kind == "sched" {
			m.quitAfter = true // do not cancel: let the change finish or restore, then quit
			return m, nil
		}
		m.cancelWork()
		m.quit = true
		return m, tea.Quit
	}

	// a confirmation dialog takes every key
	if m.dlg != nil {
		switch s {
		case "y", "Y", "enter":
			p := *m.dlg
			m.dlg = nil
			if p.kind == "run" {
				p.reason.GrantRun(&p.run)
				return m, m.startRun(p.r, p.run)
			}
			p.reason.GrantSched(&p.sched)
			return m, m.startSched(p)
		case "n", "N", "esc":
			m.dlg = nil
			m.setStatus("취소했습니다", false)
		}
		return m, nil
	}

	// while something runs, only esc (stop waiting) works
	if m.work != nil {
		if s == "esc" {
			m.work.cancel()
		}
		return m, nil
	}

	switch m.screen {
	case scForm:
		return m.keyForm(k)
	case scLogs:
		return m.keyLogs(k)
	}
	if m.searching {
		return m.keySearch(k)
	}
	return m.keyList(k)
}

func (m *Model) keySearch(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil
	case "esc":
		m.searching = false
		m.search.Blur()
		m.search.SetValue("")
		m.filter = ""
		m.refilter()
		return m, nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(k)
	m.filter = m.search.Value()
	m.cursor = 0
	m.refilter()
	return m, cmd
}

// keyList handles the list and the detail screen; they share the action keys.
func (m *Model) keyList(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	switch s {
	case "q":
		m.quit = true
		return m, tea.Quit
	case "esc", "backspace":
		if m.screen == scDetail {
			m.screen = scList
		}
		return m, nil
	}
	if m.screen == scList {
		switch s {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
			m.fixScroll()
			return m, nil
		case "down", "j":
			m.cursor = min(max(0, len(m.vis)-1), m.cursor+1)
			m.fixScroll()
			return m, nil
		case "pgup":
			m.cursor = max(0, m.cursor-m.rowsVisible())
			m.fixScroll()
			return m, nil
		case "pgdown":
			m.cursor = min(max(0, len(m.vis)-1), m.cursor+m.rowsVisible())
			m.fixScroll()
			return m, nil
		case "a":
			m.showAll = !m.showAll
			m.cursor = 0
			m.refilter()
			return m, nil
		case "/":
			// a new search starts empty; otherwise a second search would be appended to the first
			m.searching = true
			m.search.SetValue("")
			m.filter = ""
			m.cursor = 0
			m.refilter()
			m.search.Focus()
			return m, textinput.Blink
		case "enter":
			if _, ok := m.selected(); ok {
				m.screen = scDetail
			}
			return m, nil
		}
	}

	r, ok := m.selected()
	if !ok {
		return m, nil
	}
	switch s {
	case "r":
		return m, m.startRun(r, routine.RunOpts{})
	case "e":
		if err := m.be.CanEditSchedule(r); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.prev, m.screen, m.formFor, m.formErr = m.screen, scForm, r, ""
		m.form.SetValue("")
		m.form.Focus()
		return m, textinput.Blink
	case "l":
		m.logsFrom = m.screen
		m.logsGen++
		return m, m.fetchLogs(r)
	case "t":
		if m.tracking {
			m.setStatus("추적 변경이 아직 끝나지 않았습니다. 잠시 뒤에 다시 누르세요", false)
			return m, nil
		}
		m.tracking = true
		return m, func() tea.Msg {
			var changed bool
			var err error
			if r.Tracked {
				changed, err = m.be.Untrack(r.Label)
			} else {
				changed, err = m.be.Track(r.Label)
			}
			return trackMsg{r.Label, !r.Tracked, changed, err}
		}
	}
	return m, nil
}

func (m *Model) keyForm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.screen = m.prev
		m.form.Blur()
		return m, nil
	case "enter":
		edit, revert, err := schedule.ParseText(m.form.Value())
		if err != nil {
			m.formErr = err.Error()
			return m, nil
		}
		m.screen = m.prev
		m.form.Blur()
		p := pending{kind: "sched", r: m.formFor, edit: edit}
		if revert {
			p.kind = "revert"
		}
		return m, m.startSched(p)
	}
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(k)
	m.formErr = ""
	return m, cmd
}

func (m *Model) keyLogs(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		m.quit = true
		return m, tea.Quit
	case "esc", "backspace":
		m.leaveLogs()
		return m, nil
	case "f":
		m.follow = !m.follow
		m.followGen++
		if m.follow {
			return m, m.followTick()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(k)
	return m, cmd
}

// Run opens the TUI on the real machine and returns when the user quits.
func Run(ctx context.Context, env sys.Env) error {
	return result(tea.NewProgram(New(ctx, Live{Env: env}), tea.WithAltScreen()).Run())
}

// result is Run's outcome: the program's own error, else an error the final model kept for the
// caller, so the process can print it and exit non-zero after the screen is gone.
func result(final tea.Model, err error) error {
	if err != nil {
		return err
	}
	if m, ok := final.(*Model); ok {
		return m.exitErr
	}
	return nil
}
