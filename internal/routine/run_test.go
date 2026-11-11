package routine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const lbl = "dev.rtm.test.x"

// printOut fakes `launchctl print` output for a loaded service.
func printOut(state string, pid, runs int, exit string) sys.Result {
	s := fmt.Sprintf("gui/501/%s = {\n\tstate = %s\n\truns = %d\n", lbl, state, runs)
	if pid > 0 {
		s += fmt.Sprintf("\tpid = %d\n", pid)
	}
	if exit != "" {
		s += "\tlast exit code = " + exit + "\n"
	}
	return sys.Result{Out: s + "}\n"}
}

var notLoaded = sys.Result{Err: fmt.Errorf("exit status 113")}

type fix struct {
	env sys.Env
	fr  *sys.FakeRunner
}

// newFix scripts the `launchctl print` replies in order; every mutating command succeeds.
func newFix(prints ...sys.Result) fix {
	fr := &sys.FakeRunner{
		Results: map[string]sys.Result{
			"launchctl bootstrap gui/501 /p/x.plist": {},
			"launchctl kickstart gui/501/" + lbl:     {},
			"launchctl kickstart -k gui/501/" + lbl:  {},
		},
		Queue: map[string][]sys.Result{"launchctl print gui/501/" + lbl: prints},
	}
	return fix{sys.Env{UID: 501, Runner: fr}, fr}
}

func (f fix) mutations() []string {
	var m []string
	for _, c := range f.fr.Calls {
		if !strings.HasPrefix(c, "launchctl print ") {
			m = append(m, c)
		}
	}
	return m
}

func (f fix) kicked() bool { return len(f.mutations()) > 0 }

var rt = Routine{Label: lbl, Source: SourceUser, Path: "/p/x.plist"}
var fast = RunOpts{Wait: 50 * time.Millisecond, Poll: time.Millisecond}

func opts(f func(*RunOpts)) RunOpts { o := fast; f(&o); return o }

func TestRunNotLoadedWithoutLoadIsRefused(t *testing.T) {
	f := newFix(notLoaded)
	_, err := Run(context.Background(), f.env, rt, fast)
	if err == nil || !strings.Contains(err.Error(), "--load") {
		t.Fatalf("err = %v", err)
	}
	if f.kicked() {
		t.Errorf("nothing may change without --load: %v", f.mutations())
	}
}

func TestRunNotLoadedWithLoad(t *testing.T) {
	f := newFix(notLoaded, printOut("not running", 0, 0, ""), printOut("running", 7, 1, ""), printOut("not running", 0, 1, "0"))
	res, err := Run(context.Background(), f.env, rt, opts(func(o *RunOpts) { o.Load = true }))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Bootstrapped || !res.Finished || res.Status.LastExitCode == nil || *res.Status.LastExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
	want := []string{"launchctl bootstrap gui/501 /p/x.plist", "launchctl kickstart gui/501/" + lbl}
	if strings.Join(f.mutations(), "|") != strings.Join(want, "|") {
		t.Errorf("mutations = %v, want bootstrap then plain kickstart", f.mutations())
	}
}

// RunAtLoad/KeepAlive jobs are already started by bootstrap: no extra kickstart.
func TestRunBootstrapAlreadyStartedByLaunchd(t *testing.T) {
	f := newFix(notLoaded, printOut("running", 7, 1, ""), printOut("not running", 0, 1, "0"))
	res, err := Run(context.Background(), f.env, rt, opts(func(o *RunOpts) { o.Load = true }))
	if err != nil || !res.Finished || !res.Bootstrapped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if m := f.mutations(); len(m) != 1 || !strings.Contains(m[0], "bootstrap") {
		t.Errorf("mutations = %v, want bootstrap only", m)
	}
}

func TestRunLoadedNotRunningKickstarts(t *testing.T) {
	f := newFix(printOut("not running", 0, 3, "0"), printOut("not running", 0, 3, "0"), printOut("not running", 0, 4, "1"))
	res, err := Run(context.Background(), f.env, rt, fast)
	if err != nil {
		t.Fatal(err)
	}
	// the first poll still shows runs=3 (old result): not finished until the counter goes up
	if !res.Finished || *res.Status.LastExitCode != 1 || res.Status.Runs != 4 {
		t.Fatalf("res = %+v", res)
	}
	if m := f.mutations(); len(m) != 1 || m[0] != "launchctl kickstart gui/501/"+lbl {
		t.Errorf("mutations = %v", m)
	}
}

func TestRunRunningWithoutRestartIsRefused(t *testing.T) {
	f := newFix(printOut("running", 99, 1, ""))
	_, err := Run(context.Background(), f.env, rt, fast)
	if err == nil || !strings.Contains(err.Error(), "already running (pid 99)") || !strings.Contains(err.Error(), "--restart") {
		t.Fatalf("err = %v", err)
	}
	if f.kicked() {
		t.Errorf("mutations = %v", f.mutations())
	}
}

func TestRunRestart(t *testing.T) {
	f := newFix(printOut("running", 99, 1, ""), printOut("running", 100, 2, ""), printOut("not running", 0, 2, "0"))
	res, err := Run(context.Background(), f.env, rt, opts(func(o *RunOpts) { o.Restart = true }))
	if err != nil || !res.Finished {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if m := f.mutations(); len(m) != 1 || m[0] != "launchctl kickstart -k gui/501/"+lbl {
		t.Errorf("mutations = %v, want kickstart -k", m)
	}
}

func TestRunRestartOfNotRunningJobIsPlainKickstart(t *testing.T) {
	f := newFix(printOut("not running", 0, 1, "0"), printOut("not running", 0, 2, "0"))
	if _, err := Run(context.Background(), f.env, rt, opts(func(o *RunOpts) { o.Restart = true })); err != nil {
		t.Fatal(err)
	}
	if m := f.mutations(); len(m) != 1 || strings.Contains(m[0], "-k") {
		t.Errorf("mutations = %v, -k must only be used on a running job", m)
	}
}

func TestRunKeepAliveRestartNeedsYes(t *testing.T) {
	svc := rt
	svc.spec = schedule.Spec{KeepAlive: true}

	f := newFix(printOut("running", 99, 1, ""))
	_, err := Run(context.Background(), f.env, svc, opts(func(o *RunOpts) { o.Restart = true }))
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
	if f.kicked() {
		t.Errorf("mutations = %v", f.mutations())
	}

	f = newFix(printOut("running", 99, 1, ""), printOut("not running", 0, 2, "0"))
	if _, err := Run(context.Background(), f.env, svc, opts(func(o *RunOpts) { o.Restart, o.Yes = true, true })); err != nil {
		t.Fatal(err)
	}
	if m := f.mutations(); len(m) != 1 || !strings.Contains(m[0], "kickstart -k") {
		t.Errorf("mutations = %v", m)
	}

	// --yes alone does not restart a running job
	f = newFix(printOut("running", 99, 1, ""))
	if _, err := Run(context.Background(), f.env, svc, opts(func(o *RunOpts) { o.Yes = true })); err == nil {
		t.Error("--yes without --restart must still be refused")
	}
}

func TestRunStillRunningAfterWait(t *testing.T) {
	f := newFix(printOut("not running", 0, 1, "0"))
	f.fr.Results["launchctl print gui/501/"+lbl] = printOut("running", 5, 2, "") // never finishes
	res, err := Run(context.Background(), f.env, rt, fast)
	if err != nil || res.Finished || res.Status.State != "running" || res.Status.PID != 5 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunKickstartFailure(t *testing.T) {
	f := newFix(notLoaded, printOut("not running", 0, 0, ""))
	f.fr.Results["launchctl kickstart gui/501/"+lbl] = sys.Result{Err: fmt.Errorf("Could not find service")}
	res, err := Run(context.Background(), f.env, rt, opts(func(o *RunOpts) { o.Load = true }))
	if err == nil {
		t.Fatal("error expected")
	}
	if !res.Bootstrapped {
		t.Error("the caller must learn that bootstrap already happened")
	}
}

func TestRunCronIsRefused(t *testing.T) {
	f := newFix()
	_, err := Run(context.Background(), f.env, Routine{Label: "cron:0ec33761", Source: SourceCron}, fast)
	if err == nil || !strings.Contains(err.Error(), "crontab") || len(f.fr.Calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.fr.Calls)
	}
}

// Dev safety net, end to end: a real-looking label is refused before any mutating command.
func TestRunGuardProtectsRealLabels(t *testing.T) {
	t.Setenv("RTM_TEST_LABELS_ONLY", "1")
	real := Routine{Label: "ai.hermes.gateway", Source: SourceUser, Path: "/p/h.plist"}
	fr := &sys.FakeRunner{
		Results: map[string]sys.Result{},
		Queue: map[string][]sys.Result{"launchctl print gui/501/ai.hermes.gateway": {
			{Out: "\tstate = running\n\tpid = 1\n\truns = 1\n"},
			{Out: "\tstate = not running\n\truns = 1\n"},
			notLoaded,
		}},
	}
	env := sys.Env{UID: 501, Runner: fr}
	for _, o := range []RunOpts{{Restart: true, Yes: true}, {}, {Load: true}} {
		if _, err := Run(context.Background(), env, real, o); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("opts %+v: err = %v", o, err)
		}
	}
	for _, c := range fr.Calls {
		if !strings.HasPrefix(c, "launchctl print ") {
			t.Errorf("a mutating command reached the runner: %q", c)
		}
	}
}

// Real launchd sequence, seen on this Mac: after a kickstart the job sits in `xpcproxy` with a
// pid and the PREVIOUS run's exit code. That is not "finished".
func TestRunDoesNotCallAnInFlightRunFinished(t *testing.T) {
	f := newFix(
		printOut("not running", 0, 1, "0"), // before: finished once, exit 0
		printOut("xpcproxy", 4450, 2, "0"), // starting: runs already 2, exit code is the OLD one
		printOut("running", 4450, 2, "0"),  // running
		printOut("not running", 0, 2, "7"), // really done
	)
	res, err := Run(context.Background(), f.env, rt, fast)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Finished || res.Status.LastExitCode == nil || *res.Status.LastExitCode != 7 {
		t.Fatalf("res = %+v, want the final exit 7, not the stale 0", res.Status)
	}
}

// Exit code still "(never exited)" with no process: not finished, so we never print a made-up result.
func TestRunNeedsAnExitCode(t *testing.T) {
	f := newFix(printOut("not running", 0, 0, ""))
	f.fr.Results["launchctl print gui/501/"+lbl] = printOut("spawn scheduled", 0, 1, "")
	res, err := Run(context.Background(), f.env, rt, fast)
	if err != nil || res.Finished {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Codex P1: run only treated state=="running" as running, so a job in xpcproxy (pid set) was
// kickstarted without -k. run and schedule now share Status.IsRunning.
func TestRunTreatsAPidAsRunning(t *testing.T) {
	f := newFix(printOut("xpcproxy", 4450, 2, "0"), printOut("running", 4451, 3, "0"), printOut("not running", 0, 3, "0"))
	if _, err := Run(context.Background(), f.env, rt, fast); err == nil {
		t.Fatal("a job that has a live process must not be kickstarted without --restart")
	}
	if f.kicked() {
		t.Errorf("mutations = %v", f.mutations())
	}

	f = newFix(printOut("xpcproxy", 4450, 2, "0"), printOut("running", 4451, 3, "0"), printOut("not running", 0, 3, "0"))
	if _, err := Run(context.Background(), f.env, rt, opts(func(o *RunOpts) { o.Restart = true })); err != nil {
		t.Fatal(err)
	}
	if m := f.mutations(); len(m) != 1 || !strings.Contains(m[0], "kickstart -k") {
		t.Errorf("mutations = %v, a starting job must be restarted with -k", m)
	}
}

func TestConfirmErrors(t *testing.T) {
	var ce *ConfirmError

	// not loaded
	f := newFix(notLoaded)
	_, err := Run(context.Background(), f.env, rt, fast)
	if !errors.As(err, &ce) || ce.Need != NeedLoad {
		t.Fatalf("err = %#v", err)
	}
	o := fast
	ce.GrantRun(&o)
	if !o.Load || o.Restart || o.Yes {
		t.Errorf("granting load: %+v", o)
	}

	// running
	f = newFix(printOut("running", 9, 1, ""))
	_, err = Run(context.Background(), f.env, rt, fast)
	if !errors.As(err, &ce) || ce.Need != NeedRestart {
		t.Fatalf("err = %#v", err)
	}
	o = fast
	ce.GrantRun(&o)
	if !o.Restart || o.Yes {
		t.Errorf("granting restart: %+v", o)
	}

	// running KeepAlive service: both Restart and Yes in one answer
	svc := rt
	svc.spec = schedule.Spec{KeepAlive: true}
	f = newFix(printOut("running", 9, 1, ""))
	_, err = Run(context.Background(), f.env, svc, fast)
	if !errors.As(err, &ce) || ce.Need != NeedRestartKeepAlive {
		t.Fatalf("err = %#v", err)
	}
	o = fast
	ce.GrantRun(&o)
	if !o.Restart || !o.Yes {
		t.Errorf("granting restart-keepalive: %+v", o)
	}
	f = newFix(printOut("running", 9, 1, ""), printOut("not running", 0, 2, "0"))
	if _, err := Run(context.Background(), f.env, svc, o); err != nil {
		t.Errorf("after the grant the run must go through: %v", err)
	}

	// the message is still the CLI wording
	if !strings.Contains(ce.Error(), "--yes") {
		t.Errorf("message = %q", ce.Error())
	}
}

// slowRunner makes every command take d, like a slow `launchctl print`.
type slowRunner struct {
	sys.Runner
	d time.Duration
}

func (s slowRunner) Run(name string, args ...string) (string, error) {
	time.Sleep(s.d)
	return s.Runner.Run(name, args...)
}

// Codex P1: the limit was a count of sleeps, so time spent in Query was not counted.
func TestRunWaitIsARealDeadline(t *testing.T) {
	f := newFix(printOut("not running", 0, 1, "0"))
	f.fr.Results["launchctl print gui/501/"+lbl] = printOut("running", 5, 2, "") // never finishes
	f.env.Runner = slowRunner{f.fr, 40 * time.Millisecond}
	start := time.Now()
	res, err := Run(context.Background(), f.env, rt, RunOpts{Wait: 150 * time.Millisecond, Poll: 10 * time.Millisecond})
	took := time.Since(start)
	if err != nil || res.Finished {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// A sleep-count loop would run ~15 polls of (40ms query + 10ms sleep) = 750ms.
	if took > 400*time.Millisecond {
		t.Errorf("took %v for a 150ms wait: the deadline must be real time", took)
	}
}

func TestRunStopsWaitingWhenCancelled(t *testing.T) {
	f := newFix(printOut("not running", 0, 1, "0"))
	f.fr.Results["launchctl print gui/501/"+lbl] = printOut("running", 5, 2, "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()
	start := time.Now()
	res, err := Run(ctx, f.env, rt, RunOpts{Wait: 10 * time.Second, Poll: 20 * time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v: cancelling must end the wait", time.Since(start))
	}
	if !res.Started || !f.kicked() {
		t.Errorf("the job was started before the wait; res=%+v", res)
	}
}

func TestRunDoesNothingWhenAlreadyCancelled(t *testing.T) {
	f := newFix(printOut("not running", 0, 1, "0"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Run(ctx, f.env, rt, fast)
	if !errors.Is(err, context.Canceled) || res.Started || len(f.fr.Calls) != 0 {
		t.Fatalf("res=%+v err=%v calls=%v", res, err, f.fr.Calls)
	}
}
