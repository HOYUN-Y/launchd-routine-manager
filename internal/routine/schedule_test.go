package routine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"howett.net/plist"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/testutil"
)

const schedLabel = "dev.rtm.test.sched"

var ctx0 = context.Background()

// plist with an interval schedule and keys that must survive any schedule change
const schedPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>dev.rtm.test.sched</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string><string>echo hi &amp;&amp; exit 0</string></array>
<key>EnvironmentVariables</key><dict><key>API_KEY</key><string>secret</string></dict>
<key>StandardOutPath</key><string>/tmp/sched.out</string>
<key>ThrottleInterval</key><integer>30</integer>
<key>StartInterval</key><integer>3600</integer>
</dict></plist>
`

type sfix struct {
	env  sys.Env
	fr   *sys.FakeRunner
	path string
	r    Routine
}

// newSched sets up a temp home with one plist and a scripted launchd. prints are the replies
// to successive `launchctl print`; plutil, bootout and bootstrap succeed unless a test changes it.
func newSched(t *testing.T, content string, prints ...sys.Result) *sfix {
	t.Helper()
	home := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	os.MkdirAll(agents, 0o755)
	path := filepath.Join(agents, schedLabel+".plist")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fr := &sys.FakeRunner{
		Results: map[string]sys.Result{
			"plutil -lint " + path:                    {},
			"launchctl bootout gui/501/" + schedLabel: {},
			"launchctl bootstrap gui/501 " + path:     {},
			"crontab -l":                              {Err: errors.New("crontab: no crontab for me")},
		},
		Queue: map[string][]sys.Result{"launchctl print gui/501/" + schedLabel: prints},
	}
	env := sys.Env{Home: home, UID: 501, SystemAgentsDir: t.TempDir(), Runner: fr,
		Now: func() time.Time { return time.Date(2026, 11, 12, 2, 55, 30, 0, time.UTC) }}
	rs, _ := Discover(env, nil)
	r, ok := Find(rs, schedLabel)
	if !ok {
		t.Fatal("test plist not discovered")
	}
	return &sfix{env, fr, path, r}
}

// loaded fakes `launchctl print` for a loaded job holding the given schedule.
func loaded(state string, pid, interval int, cals ...schedule.Calendar) sys.Result {
	s := fmt.Sprintf("gui/501/%s = {\n\tstate = %s\n\truns = 1\n", schedLabel, state)
	if pid > 0 {
		s += fmt.Sprintf("\tpid = %d\n", pid)
	}
	if interval > 0 {
		s += fmt.Sprintf("\trun interval = %d seconds\n", interval)
	}
	if len(cals) > 0 {
		s += "\tevent triggers = {\n"
		for n, c := range cals {
			s += fmt.Sprintf("\t\t%s.%d => {\n\t\t\tstream = com.apple.launchd.calendarinterval\n\t\t\tdescriptor = {\n", schedLabel, n)
			for k, p := range map[string]*int{"Minute": c.Minute, "Hour": c.Hour, "Weekday": c.Weekday, "Day": c.Day, "Month": c.Month} {
				if p != nil {
					s += fmt.Sprintf("\t\t\t\t\"%s\" => %d\n", k, *p)
				}
			}
			s += "\t\t\t}\n\t\t}\n"
		}
		s += "\t}\n"
	}
	return sys.Result{Out: s + "}\n"}
}

func ci(n int) *int { return &n }

var (
	cal0515    = schedule.Calendar{Hour: ci(5), Minute: ci(15)}
	notLoadedS = sys.Result{Err: errors.New("exit status 113")}
)

func (f *sfix) calls() string { return strings.Join(f.fr.Calls, "\n") }

func (f *sfix) mustDaily(t *testing.T, tm string) schedule.Edit {
	t.Helper()
	e, err := schedule.Daily(tm)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (f *sfix) file(t *testing.T) []byte {
	b, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *sfix) noBackupDir(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(launchd.BackupDir(f.env.Home)); err == nil {
		t.Errorf("nothing may be saved when the change is refused")
	}
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if _, err := plist.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return m
}

func TestSetScheduleReloadsAndVerifies(t *testing.T) {
	// prints: the first look, the look right before bootout, then the confirmation
	f := newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("not running", 0, 3600), loaded("not running", 0, 0, cal0515))
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Before != "every 1h" || res.After != "daily 05:15" || !res.Reloaded || !strings.Contains(res.Verified, "launchd now holds daily 05:15") {
		t.Errorf("res = %+v", res)
	}
	want := strings.Join([]string{
		"launchctl print gui/501/" + schedLabel,
		"plutil -lint " + f.path,
		"launchctl print gui/501/" + schedLabel, // asked again right before bootout
		"launchctl bootout gui/501/" + schedLabel,
		"launchctl bootstrap gui/501 " + f.path,
		"launchctl print gui/501/" + schedLabel,
	}, "\n")
	if got := strings.Join(f.fr.Calls[1:], "\n"); got != want { // [0] is Discover's crontab -l
		t.Errorf("commands:\n%s\nwant:\n%s", got, want)
	}

	// backup = exact original, mode 0600; file = only the schedule differs
	if b, _ := os.ReadFile(res.BackupPath); string(b) != schedPlist {
		t.Errorf("backup differs from the original")
	}
	if filepath.Base(res.BackupPath) != schedLabel+"-20261112-025530.plist" {
		t.Errorf("backup name = %s", filepath.Base(res.BackupPath))
	}
	before, after := decodeMap(t, []byte(schedPlist)), decodeMap(t, f.file(t))
	delete(before, "StartInterval")
	cal := after["StartCalendarInterval"]
	delete(after, "StartCalendarInterval")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("keys other than the schedule changed:\n%#v\n%#v", before, after)
	}
	if m, ok := cal.(map[string]any); !ok || m["Hour"] != uint64(5) || m["Minute"] != uint64(15) {
		t.Errorf("calendar = %#v", cal)
	}
	if fi, _ := os.Stat(f.path); fi.Mode().Perm() != 0o600 {
		t.Errorf("plist mode = %v, must be kept", fi.Mode().Perm())
	}
}

func TestSetScheduleOnJobThatIsNotLoaded(t *testing.T) {
	f := newSched(t, schedPlist, notLoadedS)
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reloaded || !strings.Contains(res.Verified, "not loaded in launchd") {
		t.Errorf("res = %+v", res)
	}
	if strings.Contains(f.calls(), "bootout") || strings.Contains(f.calls(), "bootstrap") {
		t.Errorf("a job that was not loaded must not be loaded by a schedule change:\n%s", f.calls())
	}
	if !strings.Contains(string(f.file(t)), "StartCalendarInterval") {
		t.Error("the plist must still be updated")
	}
}

func TestSetScheduleRunningNeedsYes(t *testing.T) {
	f := newSched(t, schedPlist, loaded("running", 99, 3600))
	_, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "pid 99") {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Error("plist must be untouched")
	}
	f.noBackupDir(t)
	if strings.Contains(f.calls(), "bootout") || strings.Contains(f.calls(), "plutil") {
		t.Errorf("commands ran:\n%s", f.calls())
	}

	f = newSched(t, schedPlist, loaded("running", 99, 3600), loaded("running", 99, 3600), loaded("not running", 0, 0, cal0515))
	if _, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.calls(), "bootout") {
		t.Error("--yes must allow the reload")
	}
}

// A process that is still starting has a pid but is not yet "running"; it must count as running.
func TestSetScheduleTreatsPidAsRunning(t *testing.T) {
	f := newSched(t, schedPlist, loaded("xpcproxy", 4450, 3600))
	if _, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{}); err == nil {
		t.Fatal("a job with a live process must need --yes")
	}
}

func TestSetScheduleInexpressibleNeedsForce(t *testing.T) {
	yearly := strings.Replace(schedPlist, "<key>StartInterval</key><integer>3600</integer>",
		"<key>StartCalendarInterval</key><dict><key>Month</key><integer>3</integer><key>Day</key><integer>5</integer><key>Hour</key><integer>7</integer><key>Minute</key><integer>0</integer></dict>", 1)
	f := newSched(t, yearly, notLoadedS)
	_, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "month=3 day=5 07:00") {
		t.Fatalf("err = %v (it must show the original schedule)", err)
	}
	if string(f.file(t)) != yearly {
		t.Error("plist must be untouched")
	}
	f.noBackupDir(t)

	f = newSched(t, yearly, notLoadedS)
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{Force: true})
	if err != nil || res.Before != "month=3 day=5 07:00" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if b, _ := os.ReadFile(res.BackupPath); string(b) != yearly {
		t.Error("the inexpressible original must be in the backup")
	}
}

func TestSetScheduleSameScheduleDoesNothing(t *testing.T) {
	f := newSched(t, schedPlist)
	every, _ := schedule.Every("1h")
	res, err := SetSchedule(ctx0, f.env, f.r, every, SchedOpts{})
	if err != nil || !res.Unchanged {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	f.noBackupDir(t)
	if len(f.fr.Calls) != 1 { // only Discover's crontab -l
		t.Errorf("no launchctl at all expected: %v", f.fr.Calls)
	}
}

func TestSetScheduleLintFailureRestoresTheFile(t *testing.T) {
	f := newSched(t, schedPlist, loaded("not running", 0, 3600))
	f.fr.Results["plutil -lint "+f.path] = sys.Result{Err: errors.New("syntax error")}
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "plutil -lint rejected") || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Errorf("the original must be back, byte for byte:\n%s", f.file(t))
	}
	if b, _ := os.ReadFile(res.BackupPath); string(b) != schedPlist {
		t.Error("backup must be kept")
	}
	if strings.Contains(f.calls(), "bootout") {
		t.Errorf("launchd must not be touched after a lint failure:\n%s", f.calls())
	}
}

func TestSetScheduleBootoutFailureRestoresTheFile(t *testing.T) {
	f := newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("not running", 0, 3600))
	f.fr.Results["launchctl bootout gui/501/"+schedLabel] = sys.Result{Err: errors.New("Operation not permitted")}
	_, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "old schedule") {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist || strings.Contains(f.calls(), "bootstrap") {
		t.Errorf("file restored=%v calls:\n%s", string(f.file(t)) == schedPlist, f.calls())
	}
}

// After bootout the job is gone; if bootstrap of the new plist fails, load the old one again.
func TestSetScheduleBootstrapFailureLoadsTheOldPlistAgain(t *testing.T) {
	f := newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("not running", 0, 3600))
	key := "launchctl bootstrap gui/501 " + f.path
	delete(f.fr.Results, key)
	f.fr.Queue[key] = []sys.Result{{Err: errors.New("Bootstrap failed: 5")}, {}}
	_, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "restored and loaded again") {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Error("the old plist must be back")
	}
	if n := strings.Count(f.calls(), "bootstrap"); n != 2 {
		t.Errorf("bootstrap calls = %d, want 2 (new, then old)", n)
	}

	// both fail: say loudly that the job is not loaded
	f = newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("not running", 0, 3600))
	delete(f.fr.Results, "launchctl bootstrap gui/501 "+f.path)
	f.fr.Queue["launchctl bootstrap gui/501 "+f.path] = []sys.Result{{Err: errors.New("x")}, {Err: errors.New("y")}}
	_, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "NOT loaded") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetScheduleNotesRunAtLoad(t *testing.T) {
	withRAL := strings.Replace(schedPlist, "<key>ThrottleInterval", "<key>RunAtLoad</key><true/><key>ThrottleInterval", 1)
	f := newSched(t, withRAL, loaded("not running", 0, 3600), loaded("not running", 0, 3600), loaded("not running", 0, 0, cal0515))
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "RunAtLoad") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// RunAtLoad stays in the plist, so the summary of the result must keep it
	if res.After != "daily 05:15, at load" || !strings.Contains(res.Verified, "daily 05:15, at load") {
		t.Errorf("After = %q, Verified = %q", res.After, res.Verified)
	}
}

func TestSetScheduleRefusals(t *testing.T) {
	check := func(name string, f *sfix, r Routine, wantErr string) {
		t.Helper()
		before, _ := os.ReadFile(r.Path)
		_, err := SetSchedule(ctx0, f.env, r, f.mustDaily(t, "05:15"), SchedOpts{Yes: true, Force: true})
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, wantErr)
		}
		if after, _ := os.ReadFile(r.Path); !bytes.Equal(before, after) {
			t.Errorf("%s: the file was modified", name)
		}
		f.noBackupDir(t)
		if len(f.fr.Calls) > 1 { // 1 = Discover's crontab -l
			t.Errorf("%s: commands ran: %v", name, f.fr.Calls[1:])
		}
	}

	f := newSched(t, schedPlist)
	sys1 := f.r
	sys1.Source = SourceSystem
	check("system agent", f, sys1, "/Library/LaunchAgents")

	f = newSched(t, schedPlist)
	cr := f.r
	cr.Source = SourceCron
	check("cron entry", f, cr, "crontab")

	f = newSched(t, schedPlist)
	other := filepath.Join(t.TempDir(), schedLabel+".plist")
	os.WriteFile(other, []byte(schedPlist), 0o644)
	out := f.r
	out.Path = other
	check("outside ~/Library/LaunchAgents", f, out, "only edits plists there")

	f = newSched(t, schedPlist)
	real := filepath.Join(t.TempDir(), "real.plist")
	os.WriteFile(real, []byte(schedPlist), 0o644)
	link := filepath.Join(f.env.Home, "Library", "LaunchAgents", "dev.rtm.test.link.plist")
	os.Symlink(real, link)
	ln := f.r
	ln.Label, ln.Path = "dev.rtm.test.link", link
	check("symlink", f, ln, "symlink")
	if _, err := os.Readlink(link); err != nil {
		t.Error("the symlink must still be a symlink")
	}
}

func TestSetScheduleDevGuard(t *testing.T) {
	f := newSched(t, schedPlist)
	r := f.r
	r.Label = "ai.hermes.gateway" // the guard looks at the label; the file stays as it is
	t.Setenv("RTM_TEST_LABELS_ONLY", "1")
	_, err := SetSchedule(ctx0, f.env, r, f.mustDaily(t, "05:15"), SchedOpts{Yes: true, Force: true})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v", err)
	}
	f.noBackupDir(t) // refused before anything else, not even a backup
	if len(f.fr.Calls) > 1 {
		t.Errorf("commands ran: %v", f.fr.Calls[1:])
	}
	if _, err := RevertSchedule(ctx0, f.env, r, SchedOpts{}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("revert must be guarded too: %v", err)
	}
}

func TestBinaryPlistStaysBinary(t *testing.T) {
	m := decodeMap(t, []byte(schedPlist))
	bin, _ := plist.Marshal(m, plist.BinaryFormat)
	f := newSched(t, string(bin), notLoadedS)
	if _, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(f.file(t), []byte("bplist00")) {
		t.Errorf("binary plist was rewritten as %.12q", f.file(t))
	}
}

func TestRevertWalksBackOneStepAndKeepsTheReplacedFile(t *testing.T) {
	f := newSched(t, schedPlist, notLoadedS, notLoadedS, notLoadedS, notLoadedS)
	if _, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{}); err != nil {
		t.Fatal(err)
	}
	afterDaily := string(f.file(t))
	rs, _ := Discover(f.env, nil)
	r, _ := Find(rs, schedLabel)
	weekly, _ := schedule.Weekly("mon,wed", "06:30")
	if _, err := SetSchedule(ctx0, f.env, r, weekly, SchedOpts{}); err != nil { // same second: backup gets a -1 suffix
		t.Fatal(err)
	}
	afterWeekly := string(f.file(t))

	rs, _ = Discover(f.env, nil)
	r, _ = Find(rs, schedLabel)
	res, err := RevertSchedule(ctx0, f.env, r, SchedOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if string(f.file(t)) != afterDaily {
		t.Errorf("revert must restore the newest backup (the daily plist) exactly:\n%s", f.file(t))
	}
	if res.Before != "weekly mon 06:30, weekly wed 06:30" || res.After != "daily 05:15" {
		t.Errorf("res = %+v", res)
	}
	if !strings.Contains(filepath.Base(res.BackupPath), "-pre-revert") {
		t.Errorf("the replaced file must be kept as pre-revert: %s", res.BackupPath)
	}
	if b, _ := os.ReadFile(res.BackupPath); string(b) != afterWeekly {
		t.Error("pre-revert copy must hold the file that was replaced")
	}

	// a second revert finds the same backup and the file already equals it
	rs, _ = Discover(f.env, nil)
	r, _ = Find(rs, schedLabel)
	if again, err := RevertSchedule(ctx0, f.env, r, SchedOpts{}); err != nil || !again.Unchanged {
		t.Errorf("second revert: %+v %v", again, err)
	}
}

func TestRevertWithoutBackupAndWhenRunning(t *testing.T) {
	f := newSched(t, schedPlist)
	if _, err := RevertSchedule(ctx0, f.env, f.r, SchedOpts{}); err == nil || !strings.Contains(err.Error(), "no backup") {
		t.Errorf("err = %v", err)
	}

	f = newSched(t, schedPlist, loaded("running", 7, 3600))
	launchd.SaveBackup(f.env.Home, schedLabel, "", f.env.Clock().Add(-time.Hour), []byte(strings.Replace(schedPlist, "3600", "900", 1)))
	if _, err := RevertSchedule(ctx0, f.env, f.r, SchedOpts{}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("revert of a running job needs --yes: %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Error("file must be untouched")
	}
}

func TestRevertReloadsAndVerifies(t *testing.T) {
	f := newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("not running", 0, 3600), loaded("not running", 0, 900))
	launchd.SaveBackup(f.env.Home, schedLabel, "", f.env.Clock().Add(-time.Hour), []byte(strings.Replace(schedPlist, "3600", "900", 1)))
	res, err := RevertSchedule(ctx0, f.env, f.r, SchedOpts{})
	if err != nil || !res.Reloaded || res.After != "every 15m" || !strings.Contains(res.Verified, "every 15m") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestScheduleConfirmErrors(t *testing.T) {
	var ce *ConfirmError
	f := newSched(t, schedPlist, loaded("running", 99, 3600))
	_, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if !errors.As(err, &ce) || ce.Need != NeedReloadRunning {
		t.Fatalf("err = %#v", err)
	}
	var o SchedOpts
	ce.GrantSched(&o)
	if !o.Yes || o.Force {
		t.Errorf("granting reload-running: %+v", o)
	}

	yearly := strings.Replace(schedPlist, "<key>StartInterval</key><integer>3600</integer>",
		"<key>StartCalendarInterval</key><dict><key>Month</key><integer>3</integer><key>Hour</key><integer>7</integer><key>Minute</key><integer>0</integer></dict>", 1)
	f = newSched(t, yearly, notLoadedS)
	_, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if !errors.As(err, &ce) || ce.Need != NeedForce {
		t.Fatalf("err = %#v", err)
	}
	o = SchedOpts{}
	ce.GrantSched(&o)
	if !o.Force || o.Yes {
		t.Errorf("granting force: %+v", o)
	}
}

// run and schedule decide "is it running" the same way: a starting job (xpcproxy + pid) counts.
func TestScheduleAndRunAgreeOnRunning(t *testing.T) {
	for _, st := range []sys.Result{loaded("running", 9, 3600), loaded("xpcproxy", 4450, 3600)} {
		f := newSched(t, schedPlist, st)
		if _, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{}); err == nil {
			t.Errorf("a job with a live process must need --yes: %s", st.Out)
		}
	}
}

func TestScheduleConfirmationWaitIsCancellable(t *testing.T) {
	stale := loaded("not running", 0, 3600) // launchd never shows the new schedule
	f := newSched(t, schedPlist, stale)
	f.fr.Results["launchctl print gui/501/"+schedLabel] = stale
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	start := time.Now()
	res, err := SetSchedule(ctx, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "interrupted") || !strings.Contains(err.Error(), "the change was made") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 800*time.Millisecond {
		t.Errorf("took %v: cancelling must cut the 1s confirmation wait short", time.Since(start))
	}
	if res.BackupPath == "" || !strings.Contains(string(f.file(t)), "StartCalendarInterval") {
		t.Error("an interrupted confirmation does not undo the change; the backup path must be reported")
	}
}

func TestScheduleDoesNothingWhenAlreadyCancelled(t *testing.T) {
	f := newSched(t, schedPlist, loaded("not running", 0, 3600))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SetSchedule(ctx, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Error("plist must be untouched")
	}
	f.noBackupDir(t)
}

func TestCronEvidenceHasTheCrontabLine(t *testing.T) {
	e, _ := env(t, sys.Result{Out: testutil.Crontab(t)})
	rs, _ := Discover(e, nil)
	c, _ := Find(rs, "cron:0ec33761")
	if ev := c.Evidence[0]; ev.File != "crontab" || ev.Line != c.Line || ev.Line != 4 {
		t.Errorf("cron evidence = %+v, want line %d", ev, c.Line)
	}
	p, _ := Find(rs, "dev.rtm.test.claude-direct")
	if p.Evidence[0].Line != 0 || p.Evidence[0].File != p.Path {
		t.Errorf("plist evidence needs only the path: %+v", p.Evidence[0])
	}
}

// ---- M4 review fixes

func TestUnderSystem(t *testing.T) {
	for p, want := range map[string]bool{
		"/Library": true, "/Library/LaunchAgents": true, "/Library/LaunchAgents/x.plist": true, "/System/Library/LaunchAgents": true,
		"/Users/me/Library/LaunchAgents": false, "/LibraryX/LaunchAgents": false, "/private/var/folders/x": false,
		"/Users/me/Library": false, "/Systemic": false, "/": false,
	} {
		if got := underSystem(p); got != want {
			t.Errorf("underSystem(%q) = %v, want %v", p, got, want)
		}
	}
}

// Codex P0: the directory check compared strings, so a symlink at ~/Library/LaunchAgents (or above)
// crossed the boundary. Now the resolved paths are compared, and a resolved /Library or /System is refused.
func TestEditableResolvesSymlinks(t *testing.T) {
	mk := func(t *testing.T) (home string) {
		home = t.TempDir()
		real, err := filepath.EvalSymlinks(home) // /var -> /private/var on macOS
		if err != nil {
			t.Fatal(err)
		}
		return real
	}
	routineAt := func(path string) Routine {
		return Routine{Label: "dev.rtm.test.x", Source: SourceUser, Path: path}
	}
	env := func(home string) sys.Env { return sys.Env{Home: home} }
	writeFile := func(p string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(schedPlist), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("plain directory is allowed", func(t *testing.T) {
		home := mk(t)
		p := filepath.Join(home, "Library", "LaunchAgents", "dev.rtm.test.x.plist")
		writeFile(p)
		if err := editable(env(home), routineAt(p)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("LaunchAgents itself a symlink to another normal directory is allowed", func(t *testing.T) {
		home := mk(t)
		dot := filepath.Join(home, "dotfiles", "LaunchAgents")
		writeFile(filepath.Join(dot, "dev.rtm.test.x.plist"))
		os.MkdirAll(filepath.Join(home, "Library"), 0o755)
		os.Symlink(dot, filepath.Join(home, "Library", "LaunchAgents"))
		p := filepath.Join(home, "Library", "LaunchAgents", "dev.rtm.test.x.plist")
		if err := editable(env(home), routineAt(p)); err != nil {
			t.Fatalf("a dotfiles-style symlink must keep working: %v", err)
		}
	})

	t.Run("a parent (Library) that is a symlink is allowed", func(t *testing.T) {
		home := mk(t)
		writeFile(filepath.Join(home, "elsewhere", "LaunchAgents", "dev.rtm.test.x.plist"))
		os.Symlink(filepath.Join(home, "elsewhere"), filepath.Join(home, "Library"))
		p := filepath.Join(home, "Library", "LaunchAgents", "dev.rtm.test.x.plist")
		if err := editable(env(home), routineAt(p)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("another path into the same real directory is allowed", func(t *testing.T) {
		home := mk(t)
		p := filepath.Join(home, "Library", "LaunchAgents", "dev.rtm.test.x.plist")
		writeFile(p)
		os.Symlink(filepath.Join(home, "Library", "LaunchAgents"), filepath.Join(home, "alias"))
		if err := editable(env(home), routineAt(filepath.Join(home, "alias", "dev.rtm.test.x.plist"))); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a directory that resolves elsewhere is refused", func(t *testing.T) {
		home := mk(t)
		writeFile(filepath.Join(home, "Library", "LaunchAgents", "dev.rtm.test.x.plist"))
		other := filepath.Join(home, "other")
		writeFile(filepath.Join(other, "dev.rtm.test.x.plist"))
		os.Symlink(other, filepath.Join(home, "sneaky"))
		err := editable(env(home), routineAt(filepath.Join(home, "sneaky", "dev.rtm.test.x.plist")))
		if err == nil || !strings.Contains(err.Error(), "only edits plists there") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("LaunchAgents missing is refused", func(t *testing.T) {
		home := mk(t)
		if err := editable(env(home), routineAt(filepath.Join(home, "Library", "LaunchAgents", "x.plist"))); err == nil {
			t.Fatal("expected an error")
		}
	})

	// The bypass itself: ~/Library/LaunchAgents is a link to the system directory. The string
	// comparison was satisfied; the resolved path is under /Library and must be refused. Only
	// editable() is called (it writes nothing), and the dev guard is on as a second lock.
	t.Run("a link to /Library/LaunchAgents is refused", func(t *testing.T) {
		const sysDir = "/Library/LaunchAgents"
		entries, err := os.ReadDir(sysDir)
		if err != nil || len(entries) == 0 {
			t.Skip("no /Library/LaunchAgents on this machine")
		}
		home := mk(t)
		os.MkdirAll(filepath.Join(home, "Library"), 0o755)
		os.Symlink(sysDir, filepath.Join(home, "Library", "LaunchAgents"))
		t.Setenv("RTM_TEST_LABELS_ONLY", "1")
		p := filepath.Join(home, "Library", "LaunchAgents", entries[0].Name())
		err = editable(env(home), routineAt(p))
		if err == nil || !strings.Contains(err.Error(), "system location") {
			t.Fatalf("err = %v, want a refusal naming the system location", err)
		}
	})
}

// Codex P1: the backup is named after the Label; a Label that is not a plain file name must stop the
// change before anything is written.
func TestScheduleRefusesLabelsThatAreNotFileNames(t *testing.T) {
	for _, label := range []string{"../evil", "a/b", "..", "x..y", "a\x00b"} {
		f := newSched(t, schedPlist, loaded("not running", 0, 3600))
		r := f.r
		r.Label = label
		before := string(f.file(t))
		for name, run := range map[string]func() error{
			"set": func() error {
				_, err := SetSchedule(ctx0, f.env, r, f.mustDaily(t, "05:15"), SchedOpts{Yes: true, Force: true})
				return err
			},
			"revert": func() error { _, err := RevertSchedule(ctx0, f.env, r, SchedOpts{Yes: true}); return err },
		} {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "cannot be used in a file name") {
				t.Errorf("%s %q: err = %v", name, label, err)
			}
		}
		if string(f.file(t)) != before {
			t.Errorf("%q: the plist was modified", label)
		}
		f.noBackupDir(t)
		if len(f.fr.Calls) > 1 {
			t.Errorf("%q: commands ran: %v", label, f.fr.Calls[1:])
		}
		// nothing was written above the backup directory either
		if es, _ := filepath.Glob(filepath.Join(f.env.Home, ".local", "*")); len(es) != 0 {
			t.Errorf("%q: files created under .local: %v", label, es)
		}
	}
}

// Codex P1: "is it running" was asked once, before the file was written. A job that started in
// between was stopped by the bootout without --yes.
func TestSetScheduleRechecksRunningBeforeBootout(t *testing.T) {
	var ce *ConfirmError
	// idle when we looked, running when we are about to bootout
	f := newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("running", 77, 3600))
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if !errors.As(err, &ce) || ce.Need != NeedReloadRunning {
		t.Fatalf("err = %#v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Errorf("the plist must be put back when the change is not going ahead:\n%s", f.file(t))
	}
	if strings.Contains(f.calls(), "bootout") || strings.Contains(f.calls(), "bootstrap") {
		t.Errorf("the job must not be touched:\n%s", f.calls())
	}
	if res.BackupPath == "" {
		t.Error("the backup is kept")
	}
	if !strings.Contains(err.Error(), "pid 77") {
		t.Errorf("message = %q", err.Error())
	}

	// with --yes it goes on (the recheck only protects against a surprise)
	f = newSched(t, schedPlist, loaded("not running", 0, 3600), loaded("running", 77, 3600), loaded("not running", 0, 0, cal0515))
	res, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{Yes: true})
	if err != nil || !res.Reloaded {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	// the job vanished from launchd in between: nothing to reload, the file change stands
	f = newSched(t, schedPlist, loaded("not running", 0, 3600), notLoadedS)
	res, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err != nil || res.Reloaded || !strings.Contains(res.Verified, "not loaded in launchd") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(f.calls(), "bootout") {
		t.Errorf("a job that is no longer loaded must not be booted out:\n%s", f.calls())
	}
}

// hookRunner runs fn before the command it names, to change the world at an exact moment.
type hookRunner struct {
	sys.Runner
	before string // command prefix
	fn     func()
	done   bool
}

func (h *hookRunner) Run(name string, args ...string) (string, error) {
	if !h.done && strings.HasPrefix(strings.Join(append([]string{name}, args...), " "), h.before) {
		h.done = true
		h.fn()
	}
	return h.Runner.Run(name, args...)
}

// Codex P1: when launchd does not show the new schedule after the reload, the old plist is put back
// and loaded again; failures of that recovery are reported too.
func TestSetScheduleRestoresWhenLaunchdDoesNotShowTheChange(t *testing.T) {
	stale := loaded("not running", 0, 3600) // launchd keeps showing the old interval
	newStale := func() *sfix {
		f := newSched(t, schedPlist, stale, stale) // [first look, recheck], then the fallback below
		f.fr.Results["launchctl print gui/501/"+schedLabel] = stale
		return f
	}

	f := newStale()
	res, err := SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "does not show the new schedule") ||
		!strings.Contains(err.Error(), "original plist was restored and loaded again") {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Errorf("the original plist must be back, byte for byte:\n%s", f.file(t))
	}
	want := []string{"bootout", "bootstrap", "bootout", "bootstrap"} // new, then the original again
	var got []string
	for _, c := range f.fr.Calls {
		for _, w := range []string{"bootout", "bootstrap"} {
			if strings.HasPrefix(c, "launchctl "+w) {
				got = append(got, w)
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("reload sequence = %v, want %v", got, want)
	}
	if b, _ := os.ReadFile(res.BackupPath); string(b) != schedPlist {
		t.Error("the backup must be kept")
	}
	if !strings.Contains(err.Error(), "launchd holds the original schedule again") {
		t.Errorf("the recovery must be verified and said so: %v", err)
	}

	// the second bootstrap fails: say the job is not loaded
	f = newStale()
	key := "launchctl bootstrap gui/501 " + f.path
	delete(f.fr.Results, key)
	f.fr.Queue[key] = []sys.Result{{}, {Err: errors.New("Bootstrap failed: 5")}}
	_, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "NOT loaded") || string(f.file(t)) != schedPlist {
		t.Errorf("err = %v, file restored = %v", err, string(f.file(t)) == schedPlist)
	}

	// the second bootout fails: the file is back but launchd still holds the new schedule
	f = newStale()
	key = "launchctl bootout gui/501/" + schedLabel
	delete(f.fr.Results, key)
	f.fr.Queue[key] = []sys.Result{{}, {Err: errors.New("Operation not permitted")}}
	_, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "still holds") || string(f.file(t)) != schedPlist {
		t.Errorf("err = %v, file restored = %v", err, string(f.file(t)) == schedPlist)
	}

	// the original cannot be written back: report it and name the backup
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f = newStale()
	dir := filepath.Dir(f.path)
	f.env.Runner = &hookRunner{Runner: f.fr, before: "launchctl bootstrap", fn: func() { os.Chmod(dir, 0o500) }}
	defer os.Chmod(dir, 0o755)
	res, err = SetSchedule(ctx0, f.env, f.r, f.mustDaily(t, "05:15"), SchedOpts{})
	os.Chmod(dir, 0o755)
	if err == nil || !strings.Contains(err.Error(), "could not be restored") || !strings.Contains(err.Error(), res.BackupPath) {
		t.Fatalf("err = %v", err)
	}
}

func TestRevertRestoresWhenLaunchdDoesNotShowTheChange(t *testing.T) {
	stale := loaded("not running", 0, 3600)
	f := newSched(t, schedPlist, stale, stale)
	f.fr.Results["launchctl print gui/501/"+schedLabel] = stale
	launchd.SaveBackup(f.env.Home, schedLabel, "", f.env.Clock().Add(-time.Hour), []byte(strings.Replace(schedPlist, "3600", "900", 1)))
	_, err := RevertSchedule(ctx0, f.env, f.r, SchedOpts{})
	if err == nil || !strings.Contains(err.Error(), "original plist was restored") {
		t.Fatalf("err = %v", err)
	}
	if string(f.file(t)) != schedPlist {
		t.Error("a failed revert must leave the plist as it was")
	}
}
