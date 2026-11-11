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
	ev := Classify("x.plist", []string{"/opt/homebrew/bin/claude", "-p", "/changelog-update"}, os.ReadFile)
	if len(ev) != 1 || ev[0].Kind != "direct" || ev[0].Text != "/opt/homebrew/bin/claude" || ev[0].File != "x.plist" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestScriptWrapReportsLine(t *testing.T) {
	sp := scriptPath(t)
	ev := Classify("x.plist", []string{"/bin/zsh", sp}, os.ReadFile)
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
		if ev := Classify("x.plist", args, os.ReadFile); ev != nil {
			t.Errorf("%s: ev = %+v, want none", name, ev)
		}
	}
}

func TestShellStringAndCron(t *testing.T) {
	ev := Classify("x.plist", []string{"/bin/sh", "-c", `cd /tmp && "codex" exec "x" >> /tmp/l 2>&1`}, os.ReadFile)
	if len(ev) != 1 || ev[0].Kind != "direct" || ev[0].Text != "codex" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestScriptCodexExec(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte("echo hi\ncodex exec --full-auto 'x'\n"), nil }
	ev := Classify("x.plist", []string{"/bin/bash", "/x/job.sh"}, read)
	if len(ev) != 1 || ev[0].Line != 2 {
		t.Fatalf("ev = %+v", ev)
	}
}

// Quoted programs: "/x/claude" -p, '/x/codex' exec, in a shell string and in a script line.
func TestQuotedPrograms(t *testing.T) {
	for _, bin := range []struct{ path, tail string }{
		{"/opt/homebrew/bin/claude", `-p "x"`},
		{"/opt/homebrew/bin/claude", `--print x`},
		{"/opt/homebrew/bin/codex", `exec "x"`},
		{"claude", `-p x`},
		{"codex", `exec x`},
	} {
		for _, q := range []string{`"`, `'`, ``} {
			cmd := q + bin.path + q + " " + bin.tail

			// direct: inside a `sh -c` string (also what a crontab command looks like)
			if ev := Classify("x.plist", []string{"/bin/sh", "-c", cmd}, os.ReadFile); len(ev) != 1 || ev[0].Kind != "direct" {
				t.Errorf("direct %s: ev = %+v", cmd, ev)
			}

			// script: the call is on its own line; the file ends without a newline too
			script := "#!/bin/sh\n" + cmd
			read := func(string) ([]byte, error) { return []byte(script), nil }
			ev := Classify("x.plist", []string{"/bin/sh", "/x/job.sh"}, read)
			if len(ev) != 1 || ev[0].Kind != "script" || ev[0].Line != 2 {
				t.Errorf("script %s: ev = %+v", cmd, ev)
			}
		}
	}
}

func TestQuotedNonMatches(t *testing.T) {
	for _, line := range []string{
		`"/x/claude-wrapper" -p x`, // different program
		`"/x/claude" -prompt x`,    // -p must be the whole flag
		`"/x/codex" execute x`,
		`echo "claude says hi"`,
	} {
		read := func(string) ([]byte, error) { return []byte(line), nil }
		if ev := Classify("x.plist", []string{"/bin/sh", "/x/job.sh"}, read); ev != nil {
			t.Errorf("%q matched: %+v", line, ev)
		}
	}
}
