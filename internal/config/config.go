// Package config reads and writes the `tracked = [...]` list in ~/.config/rtm/config.toml.
// No TOML library: only that one key is understood, every other line is left untouched.
// shortcut: a `#` comment inside the tracked array is not handled; upgrade when config grows.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	trackedRe = regexp.MustCompile(`(?ms)^tracked\s*=\s*\[(.*?)\]`)
	quotedRe  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

func Path(home string) string { return filepath.Join(home, ".config", "rtm", "config.toml") }

func Load(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(string(b)), nil
}

func parse(text string) []string {
	m := trackedRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var out []string
	for _, q := range quotedRe.FindAllString(m[1], -1) {
		if s, err := strconv.Unquote(q); err == nil {
			out = append(out, s)
		}
	}
	return out
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
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	before := parse(string(old))
	after := f(slices.Clone(before))
	if slices.Equal(before, after) {
		return false, nil
	}
	quoted := make([]string, len(after))
	for i, s := range after {
		quoted[i] = strconv.Quote(s)
	}
	line := "tracked = [" + strings.Join(quoted, ", ") + "]"
	text := string(old)
	if trackedRe.MatchString(text) {
		text = trackedRe.ReplaceAllLiteralString(text, line)
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += line + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}
