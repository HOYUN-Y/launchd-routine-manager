package logs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.log")
	check := func(content string, n int, want string, wantSize int) {
		t.Helper()
		os.WriteFile(p, []byte(content), 0o644)
		lines, fi, err := Tail(p, n)
		if err != nil || strings.Join(lines, "|") != want || fi.Size() != int64(wantSize) {
			t.Errorf("Tail(%q,%d) = %q err=%v, want %q size=%d", content, n, lines, err, want, wantSize)
		}
	}
	check("", 5, "", 0)
	check("a\nb\nc\n", 2, "b|c", 6)
	check("a\nb\nc", 2, "b|c", 5) // no trailing newline
	check("a\nb\n", 10, "a|b", 4) // fewer lines than n
	check("a\nb\n", 0, "", 4)
	// only the last newline ends the last line; blank lines before it are lines (P2 from review)
	check("a\n\n\n", 3, "a||", 4)
	check("a\n\n\n", 2, "|", 4)
	check("\n", 1, "", 1)
	check("a\n\n", 1, "", 3)

	big := numbered(20000) // ~190KB: needs more than one 64KB chunk
	os.WriteFile(p, []byte(big), 0o644)
	lines, _, _ := Tail(p, 15000)
	if len(lines) != 15000 || lines[0] != "line 5001" || lines[14999] != "line 20000" {
		t.Errorf("big tail: %d lines, first %q last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	if _, _, err := Tail(filepath.Join(t.TempDir(), "nope"), 3); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}
