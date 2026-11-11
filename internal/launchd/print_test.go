package launchd

import (
	"errors"
	"os"
	"strings"
	"testing"

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
