package launchd

import (
	"errors"
	"strings"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

func TestCommandLines(t *testing.T) {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{
		"launchctl kickstart gui/501/dev.rtm.test.a":    {},
		"launchctl kickstart -k gui/501/dev.rtm.test.a": {},
		"launchctl bootstrap gui/501 /p/a.plist":        {},
		"launchctl bootout gui/501/dev.rtm.test.a":      {Err: errors.New("boom")},
	}}
	if err := Kickstart(fr, 501, "dev.rtm.test.a", false); err != nil {
		t.Error(err)
	}
	if err := Kickstart(fr, 501, "dev.rtm.test.a", true); err != nil {
		t.Error(err)
	}
	if err := Bootstrap(fr, 501, "dev.rtm.test.a", "/p/a.plist"); err != nil {
		t.Error(err)
	}
	if err := Bootout(fr, 501, "dev.rtm.test.a"); err == nil {
		t.Error("runner error must be returned")
	}
	if len(fr.Calls) != 4 {
		t.Errorf("calls = %v", fr.Calls)
	}
}

// The dev safety net: with RTM_TEST_LABELS_ONLY=1 nothing but dev.rtm.test.* can be changed,
// and a refused call never reaches the runner.
func TestGuard(t *testing.T) {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{}}
	t.Setenv(GuardEnv, "1")
	for name, err := range map[string]error{
		"kickstart": Kickstart(fr, 501, "ai.hermes.gateway", false),
		"kill":      Kickstart(fr, 501, "ai.hermes.gateway", true),
		"bootstrap": Bootstrap(fr, 501, "ai.hermes.gateway", "/p/x.plist"),
		"bootout":   Bootout(fr, 501, "ai.hermes.gateway"),
		"prefix":    Bootout(fr, 501, "xdev.rtm.test.a"),
	} {
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: err = %v, want refusal", name, err)
		}
	}
	if len(fr.Calls) != 0 {
		t.Fatalf("refused calls reached the runner: %v", fr.Calls)
	}

	fr.Results["launchctl kickstart gui/501/dev.rtm.test.ok"] = sys.Result{}
	if err := Kickstart(fr, 501, "dev.rtm.test.ok", false); err != nil {
		t.Errorf("test labels must pass: %v", err)
	}
}

func TestGuardIsOffByDefault(t *testing.T) {
	t.Setenv(GuardEnv, "")
	fr := &sys.FakeRunner{Results: map[string]sys.Result{"launchctl kickstart gui/501/ai.hermes.gateway": {}}}
	if err := Kickstart(fr, 501, "ai.hermes.gateway", false); err != nil {
		t.Errorf("the product must be able to run any label: %v", err)
	}
}
