package routine

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/testutil"
)

func env(t *testing.T, cron sys.Result) (sys.Env, *sys.FakeRunner) {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{"crontab -l": cron}}
	return sys.Env{Home: testutil.Home(t), SystemAgentsDir: t.TempDir(), Runner: fr}, fr
}

func byLabel(rs []Routine) map[string]Routine {
	m := map[string]Routine{}
	for _, r := range rs {
		m[r.Label] = r
	}
	return m
}

// M1 acceptance: 3 plists (claude direct / script wrap / unrelated) + 2 crontab lines.
func TestDiscoverClassifies(t *testing.T) {
	e, fr := env(t, sys.Result{Out: testutil.Crontab(t)})
	rs, warns := Discover(e, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	m := byLabel(rs)

	if r := m["dev.rtm.test.claude-direct"]; !r.AI || r.Evidence[0].Kind != "direct" || r.Source != SourceUser {
		t.Errorf("claude-direct = %+v", r)
	}
	if r := m["dev.rtm.test.script-wrap"]; !r.AI || r.Evidence[0].Kind != "script" || r.Evidence[0].Line != 5 {
		t.Errorf("script-wrap = %+v", r)
	}
	if r := m["dev.rtm.test.unrelated"]; r.AI || len(r.Evidence) != 0 {
		t.Errorf("unrelated = %+v (PATH mentions .codex/.claude, args do not)", r)
	}
	if r := m["cron:0ec33761"]; !r.AI || r.Source != SourceCron || r.Schedule != "0 9 * * *" {
		t.Errorf("cron:0ec33761 = %+v", r)
	}
	if r := m["cron:e89e38a4"]; r.AI {
		t.Errorf("cron:e89e38a4 = %+v", r)
	}
	if len(rs) != 6 { // 3 plists + 3 cron lines (incl. @reboot)
		t.Errorf("got %d routines", len(rs))
	}
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Label > rs[i].Label {
			t.Errorf("not sorted: %s > %s", rs[i-1].Label, rs[i].Label)
		}
	}
	if len(fr.Calls) != 1 || fr.Calls[0] != "crontab -l" {
		t.Errorf("only `crontab -l` may run in M1, got %v", fr.Calls)
	}
}

func TestTrackedForcesAI(t *testing.T) {
	e, _ := env(t, sys.Result{Err: errors.New("crontab -l: exit status 1: crontab: no crontab for me")})
	rs, warns := Discover(e, []string{"dev.rtm.test.unrelated"})
	if len(warns) != 0 {
		t.Fatalf("`no crontab` is not a warning: %v", warns)
	}
	if r := byLabel(rs)["dev.rtm.test.unrelated"]; !r.AI || !r.Tracked || r.Evidence[0].Kind != "tracked" {
		t.Errorf("unrelated = %+v", r)
	}
}

func TestCrontabFailureIsWarning(t *testing.T) {
	e, _ := env(t, sys.Result{Err: errors.New("boom")})
	rs, warns := Discover(e, nil)
	if len(warns) != 1 || len(rs) != 3 {
		t.Fatalf("rs=%d warns=%v", len(rs), warns)
	}
}

var pinned = time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)

func running(t *testing.T) sys.Result {
	b, err := os.ReadFile("../../testdata/launchctl/running.txt")
	if err != nil {
		t.Fatal(err)
	}
	return sys.Result{Out: string(b)}
}

func TestEnrichLoaded(t *testing.T) {
	e, fr := env(t, sys.Result{Out: testutil.Crontab(t)})
	e.UID, e.Now = 501, func() time.Time { return pinned }
	fr.Results["launchctl print gui/501/dev.rtm.test.claude-direct"] = running(t)

	rs, _ := Discover(e, nil)
	r, _ := Find(rs, "dev.rtm.test.claude-direct")
	Enrich(e, &r)

	if r.Status == nil || !r.Status.Loaded || r.Status.PID != 4859 || r.StateText() != "running" {
		t.Fatalf("status = %+v", r.Status)
	}
	if r.NextRun == nil || !r.NextRun.Equal(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("next = %v (plist says daily 09:00, now is 10:30)", r.NextRun)
	}
	if r.Schedule != "daily 09:00" {
		t.Errorf("schedule = %q", r.Schedule)
	}
	// plist has no log paths, so they come from launchctl print
	if r.StdoutPath != "/Users/me/.hermes/logs/gateway.log" {
		t.Errorf("stdout = %q", r.StdoutPath)
	}
	for _, c := range fr.Calls {
		if c != "crontab -l" && !strings.HasPrefix(c, "launchctl print ") {
			t.Errorf("unexpected command %q", c)
		}
	}
}

func TestEnrichNotLoadedAndCron(t *testing.T) {
	e, _ := env(t, sys.Result{Out: testutil.Crontab(t)})
	e.UID, e.Now = 501, func() time.Time { return pinned }
	rs, _ := Discover(e, nil)

	r, _ := Find(rs, "dev.rtm.test.unrelated") // fake has no launchctl reply -> print "fails"
	Enrich(e, &r)
	if r.Status == nil || r.Status.Loaded || r.StateText() != "not loaded" || r.NextRun != nil {
		t.Errorf("not loaded: %+v next=%v", r.Status, r.NextRun)
	}

	c, _ := Find(rs, "cron:0ec33761")
	Enrich(e, &c)
	if c.Status != nil || c.StateText() != "-" {
		t.Errorf("cron has no state: %+v", c.Status)
	}
}

func TestStateText(t *testing.T) {
	one, zero := 1, 0
	for want, st := range map[string]*launchd.Status{
		"running":    {Loaded: true, State: "running", PID: 5},
		"exit 1":     {Loaded: true, State: "not running", LastExitCode: &one},
		"exit 0":     {Loaded: true, State: "not running", LastExitCode: &zero},
		"loaded":     {Loaded: true, State: "spawn scheduled"},
		"not loaded": {},
	} {
		if got := (Routine{Status: st}).StateText(); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestEnrichLastRunFromLogMtime(t *testing.T) {
	e, _ := env(t, sys.Result{Out: testutil.Crontab(t)})
	e.UID, e.Now = 501, func() time.Time { return pinned }
	logf := filepath.Join(t.TempDir(), "x.log")
	os.WriteFile(logf, []byte("hi\n"), 0o644)
	mt := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	os.Chtimes(logf, mt, mt)

	r := Routine{Label: "x", Source: SourceUser, StdoutPath: logf, StderrPath: logf,
		spec: schedule.Spec{Interval: 3600}}
	Enrich(e, &r)
	if r.LastRun == nil || !r.LastRun.Equal(mt) {
		t.Fatalf("last run = %v", r.LastRun)
	}
	if r.NextRun == nil || !r.NextRun.Equal(mt.Add(time.Hour)) {
		t.Errorf("next = %v, want last+1h", r.NextRun)
	}
	if got := r.LogPaths(); len(got) != 1 {
		t.Errorf("same stdout/stderr must be one log path: %v", got)
	}
}

// IDs are stored in config.json, so the hash must never change by accident.
func TestCronIDGolden(t *testing.T) {
	for want, in := range map[string][2]string{
		"cron:0ec33761": {"0 9 * * *", `/opt/homebrew/bin/claude -p "daily summary" >> /tmp/summary.log 2>&1`},
		"cron:e89e38a4": {"30 18 * * 1-5", "/usr/local/bin/backup-home"},
	} {
		if got := CronID(in[0], in[1]); got != want {
			t.Errorf("CronID(%q, %q) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

func TestCronIDSurvivesEditsAbove(t *testing.T) {
	text := testutil.Crontab(t)
	e1, _ := env(t, sys.Result{Out: text})
	e2, _ := env(t, sys.Result{Out: "# new comment\n15 3 * * * /usr/bin/new-job\n\n" + text})
	r1, _ := Discover(e1, []string{"cron:0ec33761"})
	r2, _ := Discover(e2, []string{"cron:0ec33761"})

	a, ok1 := Find(r1, "cron:0ec33761")
	b, ok2 := Find(r2, "cron:0ec33761")
	if !ok1 || !ok2 {
		t.Fatalf("same entry must keep its ID after lines are added above (found %v, %v)", ok1, ok2)
	}
	if a.Line == b.Line {
		t.Errorf("test is vacuous: line did not move (%d)", a.Line)
	}
	if !b.Tracked || !b.AI {
		t.Errorf("tracked ID must still point at the same entry: %+v", b)
	}
	if len(r2) != len(r1)+1 {
		t.Errorf("only the new job should be added: %d vs %d", len(r2), len(r1))
	}
	// the same command at a different time is a different routine
	if CronID("0 10 * * *", "x") == CronID("0 9 * * *", "x") {
		t.Error("schedule must be part of the ID")
	}
	for _, r := range r2 {
		if r.Source == SourceCron && !regexp.MustCompile(`^cron:[0-9a-f]{8}$`).MatchString(r.Label) {
			t.Errorf("bad cron ID %q", r.Label)
		}
	}
}

func TestDirectEvidenceNamesTheFile(t *testing.T) {
	e, _ := env(t, sys.Result{Out: testutil.Crontab(t)})
	rs, _ := Discover(e, nil)
	p, _ := Find(rs, "dev.rtm.test.claude-direct")
	if ev := p.Evidence[0]; ev.Kind != "direct" || ev.File != p.Path || ev.File == "" {
		t.Errorf("plist evidence = %+v, want File = %q", ev, p.Path)
	}
	c, _ := Find(rs, "cron:0ec33761")
	if ev := c.Evidence[0]; ev.File != "crontab" {
		t.Errorf("cron evidence = %+v", ev)
	}
}
