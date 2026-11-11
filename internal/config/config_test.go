package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTrackUntrackKeepsOtherLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if got, err := Load(path); err != nil || got != nil {
		t.Fatalf("missing file: got %v, %v", got, err)
	}
	if ch, err := Track(path, "a"); err != nil || !ch {
		t.Fatalf("Track a: %v %v", ch, err)
	}
	// add an unrelated key by hand, then track more
	b, _ := os.ReadFile(path)
	os.WriteFile(path, append([]byte("# mine\nclaude_path = \"/x/claude\"\n"), b...), 0o644)
	Track(path, "b")
	if ch, _ := Track(path, "b"); ch {
		t.Error("tracking twice should be a no-op")
	}
	got, _ := Load(path)
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("tracked = %v", got)
	}
	if ch, _ := Untrack(path, "a"); !ch {
		t.Error("Untrack a should change")
	}
	if ch, _ := Untrack(path, "zzz"); ch {
		t.Error("Untrack unknown should be a no-op")
	}
	got, _ = Load(path)
	if !slices.Equal(got, []string{"b"}) {
		t.Fatalf("tracked = %v", got)
	}
	text, _ := os.ReadFile(path)
	if want := "# mine\nclaude_path = \"/x/claude\"\ntracked = [\"b\"]\n"; string(text) != want {
		t.Fatalf("file =\n%s\nwant\n%s", text, want)
	}
}

func TestParseMultilineAndEscapes(t *testing.T) {
	got := parse("tracked = [\n  \"x.y\",\n  \"q\\\"z\",\n]\n")
	if !slices.Equal(got, []string{"x.y", `q"z`}) {
		t.Fatalf("got %q", got)
	}
}
