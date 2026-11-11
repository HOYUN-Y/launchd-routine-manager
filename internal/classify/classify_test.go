package classify

import (
	"os"
	"path/filepath"
	"testing"
)

func scriptPath(t *testing.T) string {
	p, err := filepath.Abs("../../testdata/scripts/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDirect(t *testing.T) {
	ev := Classify([]string{"/opt/homebrew/bin/claude", "-p", "/changelog-update"}, os.ReadFile)
	if len(ev) != 1 || ev[0].Kind != "direct" || ev[0].Text != "/opt/homebrew/bin/claude" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestScriptWrapReportsLine(t *testing.T) {
	sp := scriptPath(t)
	ev := Classify([]string{"/bin/zsh", sp}, os.ReadFile)
	// line 2 is a comment mentioning `claude -p` and must be skipped; the real call is line 5
	if len(ev) != 1 || ev[0].Kind != "script" || ev[0].File != sp || ev[0].Line != 5 {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestNotAI(t *testing.T) {
	for name, args := range map[string][]string{
		"plain":           {"/usr/bin/true"},
		"codex-path dir":  {"/Users/me/.codex/packages/codex-path/run"},
		"claude-ish name": {"/bin/zsh", "/x/claude-daily"},
		"missing script":  {"/bin/zsh", "/nonexistent/run.sh"},
		"relative script": {"/bin/zsh", "run.sh"},
	} {
		if ev := Classify(args, os.ReadFile); ev != nil {
			t.Errorf("%s: ev = %+v, want none", name, ev)
		}
	}
}

func TestShellStringAndCron(t *testing.T) {
	ev := Classify([]string{"/bin/sh", "-c", `cd /tmp && "codex" exec "x" >> /tmp/l 2>&1`}, os.ReadFile)
	if len(ev) != 1 || ev[0].Kind != "direct" || ev[0].Text != "codex" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestScriptCodexExec(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte("echo hi\ncodex exec --full-auto 'x'\n"), nil }
	ev := Classify([]string{"/bin/bash", "/x/job.sh"}, read)
	if len(ev) != 1 || ev[0].Line != 2 {
		t.Fatalf("ev = %+v", ev)
	}
}
