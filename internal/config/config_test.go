package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	if content != "" {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestMissingFileThenTrackCreatesIt(t *testing.T) {
	p := write(t, "")
	if got, err := Load(p); err != nil || got != nil {
		t.Fatalf("missing file: got %v, %v", got, err)
	}
	if ch, err := Track(p, "a"); err != nil || !ch {
		t.Fatalf("Track a: %v %v", ch, err)
	}
	if got, _ := Load(p); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("tracked = %v", got)
	}
	if ch, _ := Track(p, "a"); ch {
		t.Error("tracking twice should be a no-op")
	}
}

// The Codex P0: a `]` inside a label used to corrupt the TOML file.
func TestBracketsAndQuotesInLabels(t *testing.T) {
	p := write(t, `{"tracked": ["a]b"]}`)
	if _, err := Track(p, "b"); err != nil {
		t.Fatal(err)
	}
	Track(p, `q"z`)
	Track(p, "x\\y")
	want := []string{"a]b", "b", `q"z`, `x\y`}
	if got, err := Load(p); err != nil || !slices.Equal(got, want) {
		t.Fatalf("tracked = %q, %v; want %q", got, err, want)
	}
	if ch, _ := Untrack(p, "a]b"); !ch {
		t.Error("Untrack a]b should change")
	}
	if got, _ := Load(p); !slices.Equal(got, want[1:]) {
		t.Fatalf("tracked = %q", got)
	}
}

func TestUnknownKeysArePreserved(t *testing.T) {
	p := write(t, `{"claude_path": "/x/claude", "nested": {"a": [1, 2, {"b": null}], "s": "]"}, "tracked": ["a"]}`)
	Track(p, "b")
	Untrack(p, "a")
	data, _ := os.ReadFile(p)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, data)
	}
	var compact func(json.RawMessage) string
	compact = func(r json.RawMessage) string {
		var v any
		json.Unmarshal(r, &v)
		b, _ := json.Marshal(v)
		return string(b)
	}
	if compact(m["claude_path"]) != `"/x/claude"` || compact(m["nested"]) != `{"a":[1,2,{"b":null}],"s":"]"}` {
		t.Errorf("unknown keys changed:\n%s", data)
	}
	if got, _ := Load(p); !slices.Equal(got, []string{"b"}) {
		t.Errorf("tracked = %v", got)
	}
}

func TestUntrackLastLeavesEmptyListNotNull(t *testing.T) {
	p := write(t, `{"tracked": ["a"]}`)
	Untrack(p, "a")
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `"tracked": []`) {
		t.Fatalf("file:\n%s", data)
	}
}

func TestUntrackUnknownIsNoOp(t *testing.T) {
	p := write(t, `{"tracked": ["a"]}`)
	before, _ := os.ReadFile(p)
	if ch, err := Untrack(p, "zzz"); ch || err != nil {
		t.Fatalf("ch=%v err=%v", ch, err)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Error("file must not be rewritten")
	}
}

// Parse failures must refuse the write and leave the file exactly as it was.
func TestBrokenFilesAreNeverOverwritten(t *testing.T) {
	for name, content := range map[string]string{
		"truncated":     `{"tracked": ["a"`,
		"not an object": `["a"]`,
		"old toml":      "tracked = [\"a]b\"]\n",
		"wrong type":    `{"tracked": "a"}`,
		"mixed list":    `{"tracked": ["a", 1]}`,
	} {
		p := write(t, content)
		if _, err := Track(p, "b"); err == nil {
			t.Errorf("%s: Track must fail", name)
		}
		if _, err := Untrack(p, "a"); err == nil {
			t.Errorf("%s: Untrack must fail", name)
		}
		if _, err := Load(p); err == nil {
			t.Errorf("%s: Load must fail", name)
		}
		if got, _ := os.ReadFile(p); string(got) != content {
			t.Errorf("%s: file was modified:\n%s", name, got)
		}
		if _, err := os.Stat(p + ".tmp"); err == nil {
			t.Errorf("%s: leftover tmp file", name)
		}
	}
}

func TestBlankFileAndNullTracked(t *testing.T) {
	for _, content := range []string{"  \n", `{"tracked": null}`, `{}`} {
		p := write(t, content)
		if got, err := Load(p); err != nil || len(got) != 0 {
			t.Fatalf("%q: %v %v", content, got, err)
		}
		if ch, err := Track(p, "a"); err != nil || !ch {
			t.Fatalf("%q: Track %v %v", content, ch, err)
		}
	}
}

// M5 review P0: the temp file used to have a fixed name, so a link planted there was written
// through, and concurrent writers shared one file.
func TestWriteDoesNotFollowASymlinkAtTheOldTempName(t *testing.T) {
	p := write(t, `{"tracked":["a"]}`)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, p+".tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := Track(p, "b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "precious" {
		t.Fatalf("the link target was overwritten: %q", got)
	}
	if got, _ := Load(p); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("tracked = %v", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestFailedWriteLeavesNoTempFile(t *testing.T) {
	p := write(t, "")
	if err := os.MkdirAll(p, 0o755); err != nil { // config.json is a directory: the rename fails
		t.Fatal(err)
	}
	if err := writeAtomic(p, []byte("{}")); err == nil {
		t.Fatal("expected an error")
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.Contains(e.Name(), "rtm-tmp") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}
