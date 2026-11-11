package routine

import (
	"errors"
	"testing"

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
	if r := m["cron:4"]; !r.AI || r.Source != SourceCron || r.Schedule != "0 9 * * *" {
		t.Errorf("cron:4 = %+v", r)
	}
	if r := m["cron:5"]; r.AI {
		t.Errorf("cron:5 = %+v", r)
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
