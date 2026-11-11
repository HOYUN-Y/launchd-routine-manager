package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
)

// View draws the screen. It only measures (lipgloss.Width) and uses the named styles and
// helpers of style.go: no colours, no borders, no style construction here.
func (m *Model) View() string {
	w, h := max(20, m.width), max(6, m.height)
	bodyH := h - 3 // header, status line, help line

	var body string
	switch {
	case m.dlg != nil:
		body = Center(w, bodyH, m.viewDialog())
	case m.screen == scDetail:
		body = m.viewDetail(w)
	case m.screen == scLogs:
		body = m.vp.View()
	case m.screen == scForm:
		body = m.viewForm(w)
	default:
		body = m.viewList(w, bodyH)
	}
	return strings.Join([]string{m.viewHeader(w), fitHeight(body, bodyH, w), m.viewStatus(w), Clip(m.viewHelp(w), w)}, "\n")
}

// fitHeight makes body exactly h lines of at most w columns, so the status and help lines
// always sit at the bottom.
func fitHeight(body string, h, w int) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			lines[i] = Clip(l, w)
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) viewHeader(w int) string {
	left := " rtm — routines on this Mac"
	right := ""
	switch {
	case m.screen == scDetail:
		right = "상세 "
	case m.screen == scLogs:
		right = "로그 "
	case m.screen == scForm:
		right = "일정 변경 "
	case m.showAll:
		right = "[a] 전체 "
	default:
		right = "[a] AI만 "
	}
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return Styles.Header.Render(Clip(left+spaces(gap)+right, w))
}

// ---- list

type cols struct{ tag, label, sched, state, next int }

const sep = 2

func columnsFor(w int) cols {
	c := cols{tag: 3, sched: 20, state: 10, next: 28}
	fixed := func() int { return c.tag + c.sched + c.state + c.next + 3*sep + sep }
	c.label = w - fixed()
	for c.label < 16 && (c.next > 14 || c.sched > 12) { // narrow terminal: squeeze the right columns first
		if c.next > 14 {
			c.next--
		} else {
			c.sched--
		}
		c.label = w - fixed()
	}
	c.label = max(8, c.label)
	return c
}

func stateOf(r routine.Routine) (string, StatusKind) {
	st := r.Status
	switch {
	case st == nil:
		return "-", StatusKindIdle // crontab keeps no state
	case !st.Loaded:
		return "미로드", StatusKindNotLoaded
	case st.IsRunning():
		return "실행 중", StatusKindRunning
	case st.LastExitCode != nil && *st.LastExitCode == 0:
		return "종료 0", StatusKindOK
	case st.LastExitCode != nil:
		return fmt.Sprintf("실패 %d", *st.LastExitCode), StatusKindFail
	}
	return "로드됨", StatusKindIdle
}

func (m *Model) nextText(r routine.Routine) string {
	if r.NextRun == nil {
		return "-"
	}
	s := schedule.Relative(*r.NextRun, m.now())
	if r.NextRunEstimated {
		s += " (estimated)"
	}
	return s
}

func (m *Model) viewList(w, h int) string {
	if m.loading {
		return Styles.Body.Render("\n  불러오는 중…")
	}
	c := columnsFor(w)
	gap := spaces(sep)
	var out []string
	out = append(out, Styles.ColumnHead.Render(spaces(c.tag)+Cell("LABEL", c.label)+gap+Cell("일정", c.sched)+gap+Cell("상태", c.state)+gap+Cell("다음 실행", c.next)))

	rows := max(1, h-1-1) // minus the column titles and the search/filter line
	if len(m.vis) == 0 {
		hint := "표시할 루틴이 없습니다."
		if !m.showAll {
			hint += " a 를 눌러 전체를 볼 수 있습니다."
		}
		out = append(out, "", Styles.Body.Render("  "+hint))
	}
	for i := m.top; i < len(m.vis) && i < m.top+rows; i++ {
		r := m.all[m.vis[i]]
		sel := i == m.cursor
		base := Styles.Row
		if sel {
			base = Styles.RowSelected
		}
		tag := "   "
		if r.AI {
			tag = "AI "
			if r.Tracked {
				tag = "AI*"
			}
		}
		stText, kind := stateOf(r)
		out = append(out, base.Render(Cell(tag, c.tag)+Cell(r.Label, c.label)+gap+Cell(r.Schedule, c.sched)+gap)+
			StatusStyle(kind, sel).Render(Cell(stText, c.state))+
			base.Render(gap+Cell(m.nextText(r), c.next)))
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	switch {
	case m.searching:
		out = append(out, Styles.Input.Render(m.search.View()))
	case m.filter != "":
		out = append(out, Styles.Label.Render("  필터: ")+Styles.Value.Render(m.filter))
	}
	return strings.Join(out, "\n")
}

// ---- detail

func (m *Model) viewDetail(w int) string {
	r, ok := m.selected()
	if !ok {
		return Styles.Body.Render("  선택한 루틴이 없습니다.")
	}
	const keyW = 12
	valW := max(10, w-keyW-3)
	var out []string
	row := func(k, v string) {
		lines := strings.Split(Wrap(v, valW), "\n")
		for i, l := range lines {
			key := ""
			if i == 0 {
				key = k
			}
			out = append(out, " "+Styles.Label.Render(Cell(key, keyW))+" "+Styles.Value.Render(l))
		}
	}
	src := r.Source
	switch {
	case r.Path != "":
		src += " (" + r.Path + ")"
	case r.Line > 0:
		src += fmt.Sprintf(" (crontab %d번째 줄)", r.Line)
	}
	class := "AI 루틴 아님"
	if r.AI {
		class = "AI 루틴"
	}
	if r.Tracked {
		class += " (수동 추적)"
	}
	out = append(out, "")
	row("라벨", r.Label)
	row("출처", src)
	row("분류", class)
	for _, e := range r.Evidence {
		row("근거", e.String())
	}
	row("일정", r.Schedule)
	row("다음 실행", m.nextText(r))
	if st := r.Status; st == nil {
		row("상태", "- (crontab 은 상태를 기록하지 않습니다)")
	} else if !st.Loaded {
		row("상태", "미로드")
	} else {
		state := st.State
		if st.PID > 0 {
			state += fmt.Sprintf(" (pid %d)", st.PID)
		}
		row("상태", state)
		row("실행 횟수", fmt.Sprint(st.Runs))
		exit := "아직 종료한 적 없음"
		if st.LastExitCode != nil {
			exit = fmt.Sprint(*st.LastExitCode)
		}
		row("마지막 종료", exit)
	}
	if r.LastRun != nil {
		row("마지막 실행", r.LastRun.Format("2006-01-02 15:04:05")+" (로그 수정 시각으로 추정)")
	}
	if r.StdoutPath != "" {
		row("stdout", r.StdoutPath)
	}
	if r.StderrPath != "" && r.StderrPath != r.StdoutPath {
		row("stderr", r.StderrPath)
	}
	row("명령", strings.Join(r.Command, " "))
	return strings.Join(out, "\n")
}

// ---- schedule form

func (m *Model) viewForm(w int) string {
	r := m.formFor
	out := []string{
		"",
		" " + Styles.Label.Render("루틴") + "  " + Styles.Value.Render(r.Label),
		" " + Styles.Label.Render("현재 일정") + "  " + Styles.Value.Render(r.Schedule),
		"",
		" " + Styles.Body.Render("새 일정을 입력하세요:"),
		"   " + Styles.Body.Render("daily 09:00  |  weekly mon,wed 09:00  |  at 09:00,18:00  |  every 30m"),
		"   " + Styles.Body.Render("revert  (가장 최근 백업으로 되돌리기)"),
		"",
		" " + Styles.Input.Render(m.form.View()),
	}
	if m.formErr != "" {
		out = append(out, "", " "+Styles.Error.Render(m.formErr))
	}
	return strings.Join(out, "\n")
}

// ---- dialog

func (m *Model) viewDialog() string {
	p := m.dlg
	var text string
	switch p.reason.Need {
	case routine.NeedLoad:
		text = "launchd 에 로드되어 있지 않습니다.\n먼저 등록(bootstrap)하고 실행할까요?"
	case routine.NeedRestart:
		text = "이미 실행 중입니다.\n재시작하면 진행 중인 작업이 중단됩니다.\n재시작할까요?"
	case routine.NeedRestartKeepAlive:
		text = "KeepAlive 상시 서비스이고 실행 중입니다.\n재시작하면 서비스가 잠시 중단됩니다.\n재시작할까요?"
	case routine.NeedReloadRunning:
		text = "실행 중입니다.\n일정을 바꾸려면 다시 로드해야 하고\n실행 중인 작업이 중단됩니다.\n계속할까요?"
	case routine.NeedForce:
		text = "현재 일정은 rtm 이 표현할 수 없는 형식입니다.\n덮어쓰면 그 내용이 사라집니다.\n(이전 plist 는 백업됩니다)\n덮어쓸까요?"
	default:
		text = p.reason.Error()
	}
	title := Styles.DialogTitle.Render(p.r.Label)
	keys := Styles.HelpKey.Render("[y]") + Styles.DialogTitle.Render(" 예    ") + Styles.HelpKey.Render("[n]") + Styles.DialogTitle.Render(" 아니오")
	return Styles.Dialog.Render(title + "\n\n" + text + "\n\n" + keys)
}

// ---- status and help lines

// progressBar is the wait bar: one cell per second, filled as time passes.
func progressBar(done, total int) string {
	done = max(0, min(total, done))
	return Styles.Progress.Render(strings.Repeat("■", done)) + Styles.ProgressEmpty.Render(strings.Repeat("□", total-done))
}

func (m *Model) viewStatus(w int) string {
	if wk := m.work; wk != nil {
		if wk.kind == "sched" {
			return Styles.Notice.Render(Clip(" "+wk.label+" 일정 변경 중…", w))
		}
		elapsed := time.Since(wk.started)
		per := m.wait / progressCells
		done := 0
		if per > 0 {
			done = int(elapsed / per)
		}
		return Styles.Notice.Render(" "+wk.label+" 실행 중… ") + "[" + progressBar(done, progressCells) + "]" +
			Styles.Notice.Render(fmt.Sprintf(" %d/%s초", min(done, progressCells), secs(m.wait)))
	}
	if m.status == "" {
		return ""
	}
	st := Styles.Notice
	if m.statusIs {
		st = Styles.Error
	}
	return st.Render(Clip(" "+strings.ReplaceAll(m.status, "\n", " "), w))
}

func (m *Model) viewHelp(w int) string {
	var pairs [][2]string
	switch {
	case m.dlg != nil:
		pairs = [][2]string{{"y", "예"}, {"n", "아니오"}}
	case m.work != nil:
		pairs = [][2]string{{"esc", "대기 중단 (이미 시작한 작업은 계속 실행됩니다)"}, {"ctrl+c", "종료"}}
	case m.screen == scForm:
		pairs = [][2]string{{"enter", "적용"}, {"esc", "취소"}}
	case m.screen == scLogs:
		f := "꺼짐"
		if m.follow {
			f = "켜짐"
		}
		pairs = [][2]string{{"esc", "뒤로"}, {"↑↓", "스크롤"}, {"f", "따라가기 " + f}, {"q", "종료"}}
	case m.screen == scDetail:
		pairs = [][2]string{{"esc", "목록"}, {"r", "지금 실행"}, {"e", "일정 변경"}, {"l", "로그"}, {"t", "추적 지정"}, {"q", "종료"}}
	case m.searching:
		pairs = [][2]string{{"enter", "확정"}, {"esc", "검색 해제"}}
	default:
		pairs = [][2]string{{"enter", "상세"}, {"r", "지금 실행"}, {"e", "일정 변경"}, {"l", "로그"}, {"t", "추적 지정"}, {"/", "검색"}, {"a", "AI만/전체"}, {"q", "종료"}}
	}
	var parts []string
	for _, p := range pairs {
		parts = append(parts, Styles.HelpKey.Render(p[0])+" "+Styles.Help.Render(p[1]))
	}
	return " " + strings.Join(parts, Styles.Help.Render("  "))
}
