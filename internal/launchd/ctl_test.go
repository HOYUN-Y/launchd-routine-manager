package launchd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestWritePlistIsAtomicAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dev.rtm.test.a.plist")
	os.WriteFile(p, []byte("old"), 0o600)
	if err := WritePlist("dev.rtm.test.a", p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("content = %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, the old file's mode must be kept", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Errorf("temp file left behind: %v", left)
	}
}

func TestWritePlistGuard(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ai.hermes.gateway.plist")
	os.WriteFile(p, []byte("keep"), 0o644)
	t.Setenv(GuardEnv, "1")
	if err := WritePlist("ai.hermes.gateway", p, []byte("clobber")); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "keep" {
		t.Errorf("a refused write must not touch the file: %q", b)
	}
	if err := WritePlist("dev.rtm.test.ok", p, []byte("fine")); err != nil {
		t.Errorf("test labels must pass: %v", err)
	}
}

func TestLint(t *testing.T) {
	fr := &sys.FakeRunner{Results: map[string]sys.Result{
		"plutil -lint /p/a.plist": {},
		"plutil -lint /p/b.plist": {Err: errors.New("bad")},
	}}
	if Lint(fr, "/p/a.plist") != nil || Lint(fr, "/p/b.plist") == nil {
		t.Error("Lint must pass the runner's verdict through")
	}
}

func TestBackups(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 11, 12, 2, 55, 30, 0, time.UTC)
	p1, err := SaveBackup(home, "dev.rtm.test.a", "", at, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "dev.rtm.test.a-20261112-025530.plist" {
		t.Errorf("name = %s", filepath.Base(p1))
	}
	if fi, _ := os.Stat(p1); fi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v (plists can hold secrets)", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(BackupDir(home)); fi.Mode().Perm() != 0o700 {
		t.Errorf("backup dir mode = %v", fi.Mode().Perm())
	}
	p2, _ := SaveBackup(home, "dev.rtm.test.a", "", at, []byte("two")) // same second
	p3, _ := SaveBackup(home, "dev.rtm.test.a", "", at, []byte("three"))
	if filepath.Base(p2) != "dev.rtm.test.a-20261112-025530-1.plist" || filepath.Base(p3) != "dev.rtm.test.a-20261112-025530-2.plist" {
		t.Errorf("collisions: %s %s", filepath.Base(p2), filepath.Base(p3))
	}
	if b, _ := os.ReadFile(p1); string(b) != "one" {
		t.Error("an existing backup must never be overwritten")
	}

	// the newest wins: later second beats suffix, higher suffix beats lower
	if got, _ := LatestBackup(home, "dev.rtm.test.a"); got != p3 {
		t.Errorf("latest = %s, want %s", got, p3)
	}
	later, _ := SaveBackup(home, "dev.rtm.test.a", "", at.Add(time.Second), []byte("later"))
	if got, _ := LatestBackup(home, "dev.rtm.test.a"); got != later {
		t.Errorf("latest = %s, want %s", got, later)
	}

	// not confused by pre-revert copies or by labels that share a prefix
	SaveBackup(home, "dev.rtm.test.a", "pre-revert", at.Add(time.Hour), []byte("x"))
	SaveBackup(home, "dev.rtm.test.a-b", "", at.Add(2*time.Hour), []byte("x"))
	SaveBackup(home, "dev.rtm.test.ab", "", at.Add(3*time.Hour), []byte("x"))
	if got, _ := LatestBackup(home, "dev.rtm.test.a"); got != later {
		t.Errorf("latest = %s, want %s (pre-revert copies and other labels do not count)", got, later)
	}
	if got, _ := LatestBackup(home, "dev.rtm.test.a-b"); !strings.Contains(got, "a-b-") {
		t.Errorf("a-b latest = %s", got)
	}
	if _, err := LatestBackup(home, "dev.rtm.test.none"); err == nil || !strings.Contains(err.Error(), "no backup") {
		t.Errorf("err = %v", err)
	}
	if _, err := LatestBackup(t.TempDir(), "x"); err == nil {
		t.Error("no backup directory at all must be an error, not a crash")
	}
}
