// Package testutil builds fake homes from testdata/ for tests. Tests only.
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Testdata returns the absolute path of the repo's testdata dir.
func Testdata(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../testdata")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Home returns a temp home whose Library/LaunchAgents holds testdata/LaunchAgents/*.plist,
// with {{TESTDATA}} replaced by the real testdata path (plists need absolute script paths).
func Home(t *testing.T) string {
	t.Helper()
	td := Testdata(t)
	home := t.TempDir()
	dst := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(td, "LaunchAgents", "*.plist"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b = []byte(strings.ReplaceAll(string(b), "{{TESTDATA}}", td))
		if err := os.WriteFile(filepath.Join(dst, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// Crontab returns testdata/crontab.txt.
func Crontab(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Testdata(t), "crontab.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
