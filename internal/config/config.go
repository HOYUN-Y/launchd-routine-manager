// Package config reads and writes ~/.config/rtm/config.json: {"tracked": ["label", ...]}.
// Keys rtm does not know are kept byte-for-byte (as raw JSON). A file that does not parse is
// never overwritten: every write fails until the user fixes it.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

func Path(home string) string { return filepath.Join(home, ".config", "rtm", "config.json") }

// read returns the top-level object of the file (empty if the file is missing or blank).
func read(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w (fix or delete the file; rtm will not overwrite it)", path, err)
	}
	if m == nil { // the file said `null`
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

func tracked(path string, m map[string]json.RawMessage) ([]string, error) {
	var out []string
	if raw, ok := m["tracked"]; ok {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf(`%s: "tracked" must be a list of strings: %w`, path, err)
		}
	}
	return out, nil
}

func Load(path string) ([]string, error) {
	m, err := read(path)
	if err != nil {
		return nil, err
	}
	return tracked(path, m)
}

// Track adds label; it reports whether the list changed.
func Track(path, label string) (bool, error) {
	return edit(path, func(l []string) []string {
		if slices.Contains(l, label) {
			return l
		}
		return append(l, label)
	})
}

// Untrack removes label; it reports whether the list changed.
func Untrack(path, label string) (bool, error) {
	return edit(path, func(l []string) []string {
		return slices.DeleteFunc(l, func(s string) bool { return s == label })
	})
}

func edit(path string, f func([]string) []string) (bool, error) {
	m, err := read(path)
	if err != nil {
		return false, err
	}
	before, err := tracked(path, m)
	if err != nil {
		return false, err
	}
	after := f(slices.Clone(before))
	if slices.Equal(before, after) {
		return false, nil
	}
	if after == nil {
		after = []string{} // "[]", not "null"
	}
	m["tracked"], err = json.Marshal(after)
	if err != nil {
		return false, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}
