// Package classify decides whether a command is an AI routine (PLAN §3) and says why.
package classify

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type Evidence struct {
	Kind string `json:"kind"` // direct | script | tracked
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

func (e Evidence) String() string {
	switch e.Kind {
	case "script":
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Text)
	case "direct":
		return "direct: " + e.Text
	}
	return e.Kind
}

var (
	scriptExt = map[string]bool{".sh": true, ".zsh": true, ".bash": true, ".py": true}
	aiCall    = regexp.MustCompile(`\b(claude\s+(-p|--print)\b|codex\s+exec\b)`)
)

// Classify returns evidence that args run claude/codex; nil means not an AI routine.
// Rule 1: some token is a claude/codex executable. Rule 2 (only if rule 1 fails): the first
// script among the tokens contains `claude -p`, `claude --print` or `codex exec`.
// Only args are inspected, never EnvironmentVariables (PATH often mentions ".codex/...").
// shortcut: tokens are split on whitespace, so `echo claude` counts as a hit; upgrade if it bites.
func Classify(args []string, readFile func(string) ([]byte, error)) []Evidence {
	var tokens []string
	for _, a := range args {
		for _, t := range strings.Fields(a) {
			tokens = append(tokens, strings.Trim(t, `"'`))
		}
	}
	var ev []Evidence
	for _, t := range tokens {
		if b := filepath.Base(t); b == "claude" || b == "codex" {
			ev = append(ev, Evidence{Kind: "direct", Text: t})
		}
	}
	if ev != nil {
		return ev
	}
	for _, t := range tokens {
		if !scriptExt[filepath.Ext(t)] {
			continue
		}
		data, err := readFile(t)
		if err != nil {
			return nil // unreadable or relative script: no evidence, not an error
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "#") && aiCall.MatchString(line) {
				ev = append(ev, Evidence{Kind: "script", File: t, Line: n, Text: line})
			}
		}
		return ev
	}
	return nil
}
