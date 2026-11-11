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

func plistWithLabel(label string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>` + label + `</string></dict></plist>`)
}

func TestWritePlistGuard(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(GuardEnv, "1")

	// 1) the label argument is not a test label
	real := filepath.Join(dir, "ai.hermes.gateway.plist")
	os.WriteFile(real, plistWithLabel("ai.hermes.gateway"), 0o644)
	if err := WritePlist("ai.hermes.gateway", real, plistWithLabel("ai.hermes.gateway")); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v", err)
	}

	// 2) a test label as the argument, but the file being replaced is a real job's plist
	if err := WritePlist("dev.rtm.test.safe", real, plistWithLabel("dev.rtm.test.safe")); err == nil || !strings.Contains(err.Error(), `has Label "ai.hermes.gateway"`) {
		t.Fatalf("a real plist must not be overwritten through a test label: %v", err)
	}
	if b, _ := os.ReadFile(real); string(b) != string(plistWithLabel("ai.hermes.gateway")) {
		t.Errorf("a refused write must not touch the file: %q", b)
	}

	// 3) the file is a test plist but the new content would make it a real job
	mine := filepath.Join(dir, "dev.rtm.test.a.plist")
	os.WriteFile(mine, plistWithLabel("dev.rtm.test.a"), 0o644)
	if err := WritePlist("dev.rtm.test.a", mine, plistWithLabel("ai.hermes.gateway")); err == nil || !strings.Contains(err.Error(), "does not have Label") {
		t.Fatalf("content with another Label must be refused: %v", err)
	}
	if err := WritePlist("dev.rtm.test.a", mine, []byte("junk")); err == nil {
		t.Fatal("content that is not a plist must be refused")
	}
	if b, _ := os.ReadFile(mine); string(b) != string(plistWithLabel("dev.rtm.test.a")) {
		t.Error("refused writes must not touch the file")
	}

	// 4) the normal case: test label, test file, test content; and creating a new test plist
	if err := WritePlist("dev.rtm.test.a", mine, plistWithLabel("dev.rtm.test.a")); err != nil {
		t.Errorf("test plist must pass: %v", err)
	}
	fresh := filepath.Join(dir, "dev.rtm.test.new.plist")
	if err := WritePlist("dev.rtm.test.new", fresh, plistWithLabel("dev.rtm.test.new")); err != nil {
		t.Errorf("creating a test plist must pass: %v", err)
	}

	// 5) an unparsable existing file is refused (cannot prove it is a test plist)
	broken := filepath.Join(dir, "dev.rtm.test.broken.plist")
	os.WriteFile(broken, []byte("junk"), 0o644)
	if err := WritePlist("dev.rtm.test.broken", broken, plistWithLabel("dev.rtm.test.broken")); err == nil {
		t.Error("an existing file that does not parse must be refused under the guard")
	}
}

// Codex P0: the guard looked only at the label argument, so a test label could be used to
// bootstrap any plist file.
func TestBootstrapGuardReadsThePlistFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(GuardEnv, "1")
	mk := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if content != nil {
			os.WriteFile(p, content, 0o644)
		}
		return p
	}
	good := mk("dev.rtm.test.safe.plist", plistWithLabel("dev.rtm.test.safe"))
	fr := &sys.FakeRunner{Results: map[string]sys.Result{"launchctl bootstrap gui/501 " + good: {}}}

	for name, path := range map[string]string{
		"file is a real job":          mk("real.plist", plistWithLabel("ai.hermes.gateway")),
		"file has another test label": mk("other.plist", plistWithLabel("dev.rtm.test.other")),
		"file is not a plist":         mk("junk.plist", []byte("junk")),
		"file is missing":             mk("missing.plist", nil),
	} {
		if err := Bootstrap(fr, 501, "dev.rtm.test.safe", path); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(fr.Calls) != 0 {
		t.Fatalf("refused bootstraps reached the runner: %v", fr.Calls)
	}
	if err := Bootstrap(fr, 501, "dev.rtm.test.safe", good); err != nil {
		t.Errorf("the matching test plist must pass: %v", err)
	}

	// the guard off: the product bootstraps whatever it is told to
	t.Setenv(GuardEnv, "")
	fr2 := &sys.FakeRunner{Results: map[string]sys.Result{"launchctl bootstrap gui/501 /x/real.plist": {}}}
	if err := Bootstrap(fr2, 501, "ai.hermes.gateway", "/x/real.plist"); err != nil {
		t.Errorf("guard off: %v", err)
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

// Codex P0: the temp file had a fixed name. If something put a symlink there, os.WriteFile followed
// it and overwrote the link's target (and Chmod changed its permissions).
func TestWritePlistDoesNotFollowASymlinkTmp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dev.rtm.test.a.plist")
	os.WriteFile(target, []byte("old"), 0o600)
	victim := filepath.Join(t.TempDir(), "victim.txt")
	os.WriteFile(victim, []byte("precious"), 0o644)
	// the old fixed name, planted as a symlink to the victim
	if err := os.Symlink(victim, filepath.Join(dir, ".dev.rtm.test.a.plist.rtm-tmp")); err != nil {
		t.Fatal(err)
	}

	if err := WritePlist("dev.rtm.test.a", target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("the symlink target was overwritten: %q", b)
	}
	if fi, _ := os.Stat(victim); fi.Mode().Perm() != 0o644 {
		t.Errorf("the symlink target's permissions were changed to %v", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Errorf("target mode = %v", fi.Mode().Perm())
	}
}

// A temp file that cannot be moved into place must not be left behind.
func TestWritePlistRemovesItsTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.rtm.test.a.plist")
	os.Mkdir(path, 0o755) // rename of a file onto a directory fails
	if err := WritePlist("dev.rtm.test.a", path, []byte("x")); err == nil {
		t.Fatal("expected an error")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Errorf("temp file left behind: %v", left)
	}
}

// Two writers never share a temp name.
func TestWritePlistTempNamesAreExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.rtm.test.a.plist")
	os.WriteFile(path, []byte("0"), 0o644)
	for i := 0; i < 20; i++ {
		if err := WritePlist("dev.rtm.test.a", path, []byte(strings.Repeat("x", i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestCheckFileLabel(t *testing.T) {
	for _, ok := range []string{"dev.rtm.test.a", "com.example.job", "a-b_c", "with space", "ai.hermes.gateway", "x.y.z.1"} {
		if err := CheckFileLabel(ok); err != nil {
			t.Errorf("%q should be fine: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", "../x", "a/b", "/abs", "a/../b", "x..y", "a\x00b", "."} {
		if err := CheckFileLabel(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// Codex P1: a Label with a path separator or ".." put the backup outside the backups directory.
func TestBackupNeverLeavesTheBackupDirectory(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 11, 12, 2, 55, 30, 0, time.UTC)
	for _, bad := range []string{"../evil", "a/b", "..", "", "x\x00y", "../../escape"} {
		if p, err := SaveBackup(home, bad, "", at, []byte("secret")); err == nil {
			t.Errorf("label %q: backup was written to %s", bad, p)
		}
		if _, err := LatestBackup(home, bad); err == nil {
			t.Errorf("label %q: LatestBackup must refuse too", bad)
		}
	}
	// nothing was created anywhere under home except (possibly) the backups directory itself
	var found []string
	filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("files were created: %v", found)
	}
	// a normal label still works and lands inside
	p, err := SaveBackup(home, "dev.rtm.test.ok", "", at, []byte("x"))
	if err != nil || filepath.Dir(p) != BackupDir(home) {
		t.Errorf("p=%s err=%v", p, err)
	}
}
