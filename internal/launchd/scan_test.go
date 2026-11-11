package launchd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/testutil"
)

func TestScan(t *testing.T) {
	dir := filepath.Join(testutil.Home(t), "Library", "LaunchAgents")
	os.WriteFile(filepath.Join(dir, "broken.plist"), []byte("not a plist"), 0o644)
	os.WriteFile(filepath.Join(dir, "nolabel.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>RunAtLoad</key><true/></dict></plist>`), 0o644)
	os.WriteFile(filepath.Join(dir, "program.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>p</string><key>Program</key><string>/bin/x</string><key>ProgramArguments</key><array><string>x</string><string>-v</string></array></dict></plist>`), 0o644)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x"), 0o644)

	got, warns := Scan(dir)
	if len(warns) != 2 {
		t.Errorf("warnings = %v, want 2 (broken, nolabel)", warns)
	}
	labels := map[string]Plist{}
	for _, p := range got {
		labels[p.Label] = p
	}
	if len(labels) != 4 { // 3 testdata plists + program
		t.Fatalf("labels = %v", labels)
	}
	if a := labels["p"].Args; len(a) != 3 || a[0] != "/bin/x" || a[1] != "x" {
		t.Errorf("Program should be prepended: %v", a)
	}
}

func TestScanMissingDir(t *testing.T) {
	if got, warns := Scan(filepath.Join(t.TempDir(), "nope")); got != nil || warns != nil {
		t.Fatalf("got %v %v", got, warns)
	}
}
