package launchd

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

func sample(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/launchctl/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParsePrintRunning(t *testing.T) {
	st := ParsePrint(sample(t, "running.txt"))
	if !st.Loaded || st.State != "running" || st.PID != 4859 || st.Runs != 1 {
		t.Fatalf("st = %+v", st)
	}
	if st.LastExitCode != nil {
		t.Errorf("(never exited) must be nil, got %d", *st.LastExitCode)
	}
	if st.StdoutPath != "/Users/me/.hermes/logs/gateway.log" || st.StderrPath != "/Users/me/.hermes/logs/gateway.error.log" {
		t.Errorf("paths = %q %q", st.StdoutPath, st.StderrPath)
	}
}

// exited.txt is a real capture hand-edited to `state = not running`, `last exit code = 1`.
func TestParsePrintExited(t *testing.T) {
	st := ParsePrint(sample(t, "exited.txt"))
	if !st.Loaded || st.State != "not running" || st.PID != 0 || st.Runs != 1 {
		t.Fatalf("st = %+v", st)
	}
	if st.LastExitCode == nil || *st.LastExitCode != 1 {
		t.Errorf("last exit = %v, want 1", st.LastExitCode)
	}
}

func TestParsePrintExitCodeFormats(t *testing.T) {
	for in, want := range map[string]int{"0": 0, "1": 1, "78: EX_CONFIG": 78} {
		st := ParsePrint("svc = {\n\tlast exit code = " + in + "\n}\n")
		if st.LastExitCode == nil || *st.LastExitCode != want {
			t.Errorf("%q -> %v, want %d", in, st.LastExitCode, want)
		}
	}
}

func TestParsePrintIgnoresNestedBlocks(t *testing.T) {
	st := ParsePrint("svc = {\n\tstate = running\n\tenvironment = {\n\t\tstate => bogus\n\t\tpid = 999\n\t}\n\t\tstate = nested\n}\n")
	if st.State != "running" || st.PID != 0 {
		t.Fatalf("nested keys leaked: %+v", st)
	}
}

func TestQueryNotLoaded(t *testing.T) {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{
		"launchctl print gui/501/dev.rtm.test.nonexistent": {
			Out: sample(t, "not_loaded.txt"), Err: errors.New("exit status 113"),
		},
	}}
	st := Query(fr, 501, "dev.rtm.test.nonexistent")
	if st.Loaded || st.State != "" || st.PID != 0 {
		t.Fatalf("st = %+v", st)
	}
}

func TestQueryOnlyPrints(t *testing.T) {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{
		"launchctl print gui/501/ai.hermes.gateway": {Out: sample(t, "running.txt")},
	}}
	if st := Query(fr, 501, "ai.hermes.gateway"); !st.Loaded || st.PID != 4859 {
		t.Fatalf("st = %+v", st)
	}
	for _, c := range fr.Calls {
		if !strings.HasPrefix(c, "launchctl print ") {
			t.Errorf("M2 may only run `launchctl print`, ran %q", c)
		}
	}
}

// Formats seen on a real Mac: `event triggers` entries on the calendarinterval stream, and a
// top-level `run interval`.
func TestParsePrintLoadedSchedule(t *testing.T) {
	text := "gui/501/x = {\n\tstate = not running\n\trun interval = 3600 seconds\n\tevent triggers = {\n" +
		"\t\tx.268435468 => {\n\t\t\tkeepalive = 0\n\t\t\tstream = com.apple.launchd.calendarinterval\n\t\t\tdescriptor = {\n" +
		"\t\t\t\t\"Hour\" => 3\n\t\t\t\t\"Weekday\" => 4\n\t\t\t}\n\t\t}\n" +
		"\t\tx.268435467 => {\n\t\t\tstream = com.apple.launchd.calendarinterval\n\t\t\tdescriptor = {\n" +
		"\t\t\t\t\"Minute\" => 37\n\t\t\t\t\"Hour\" => 19\n\t\t\t}\n\t\t}\n" +
		"\t\tother => {\n\t\t\tstream = com.apple.fsevents.matching\n\t\t\tdescriptor = {\n\t\t\t\t\"Hour\" => 99\n\t\t\t}\n\t\t}\n" +
		"\t}\n}\n"
	st := ParsePrint(text)
	if st.RunInterval != 3600 {
		t.Errorf("interval = %d", st.RunInterval)
	}
	if len(st.Calendars) != 2 {
		t.Fatalf("calendars = %+v (a descriptor on another stream must not count)", st.Calendars)
	}
	want := []schedule.Calendar{{Hour: i(3), Weekday: i(4)}, {Hour: i(19), Minute: i(37)}}
	if !schedule.SameCalendars(st.Calendars, want) {
		t.Errorf("calendars = %+v", st.Calendars)
	}
}

// The real captures: hermes has no schedule at all; the atlas capture is a StartInterval job.
func TestParsePrintLoadedScheduleInRealSamples(t *testing.T) {
	if st := ParsePrint(sample(t, "running.txt")); len(st.Calendars) != 0 || st.RunInterval != 0 {
		t.Errorf("running.txt: %+v", st)
	}
	if st := ParsePrint(sample(t, "exited.txt")); len(st.Calendars) != 0 || st.RunInterval != 3600 {
		t.Errorf("exited.txt: calendars=%v interval=%d, want none and 3600", st.Calendars, st.RunInterval)
	}
}
