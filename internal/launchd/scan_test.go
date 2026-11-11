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

func TestScanReadsScheduleAndLogs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "s.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict>
<key>Label</key><string>s</string>
<key>StartCalendarInterval</key><array>
  <dict><key>Hour</key><integer>9</integer><key>Minute</key><integer>0</integer></dict>
  <dict><key>Hour</key><integer>18</integer><key>Minute</key><integer>0</integer></dict>
</array>
<key>StartInterval</key><integer>1800</integer>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>StandardOutPath</key><string>/tmp/o.log</string>
<key>StandardErrorPath</key><string>/tmp/e.log</string>
</dict></plist>`), 0o644)
	os.WriteFile(filepath.Join(dir, "single.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict>
<key>Label</key><string>single</string>
<key>StartCalendarInterval</key><dict><key>Weekday</key><integer>7</integer><key>Hour</key><integer>8</integer><key>Minute</key><integer>0</integer></dict>
<key>KeepAlive</key><false/>
</dict></plist>`), 0o644)

	got, warns := Scan(dir)
	if len(warns) != 0 || len(got) != 2 {
		t.Fatalf("got %d, warns %v", len(got), warns)
	}
	by := map[string]Plist{got[0].Label: got[0], got[1].Label: got[1]}
	s := by["s"]
	if d := s.Schedule.Describe(); d != "daily 09:00, daily 18:00, every 30m, always" {
		t.Errorf("describe = %q", d)
	}
	if s.StdoutPath != "/tmp/o.log" || s.StderrPath != "/tmp/e.log" {
		t.Errorf("paths = %q %q", s.StdoutPath, s.StderrPath)
	}
	if d := by["single"].Schedule.Describe(); d != "weekly sun 08:00" {
		t.Errorf("single = %q (Weekday 7 is Sunday, KeepAlive false is off)", d)
	}
}
