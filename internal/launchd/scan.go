// Package launchd reads LaunchAgent plists. M1 is read-only.
package launchd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"howett.net/plist"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
)

type Plist struct {
	Label      string
	Path       string
	Args       []string // Program (if set) followed by ProgramArguments
	Schedule   schedule.Spec
	StdoutPath string
	StderrPath string
}

type raw struct {
	Label                 string
	Program               string
	ProgramArguments      []string
	StartCalendarInterval any // dict or array of dicts
	StartInterval         int
	RunAtLoad             bool
	KeepAlive             any // bool or dict of conditions
	StandardOutPath       string
	StandardErrorPath     string
}

// Scan reads every *.plist in dir. A missing dir is empty; unreadable files are skipped
// with a warning.
func Scan(dir string) (plists []Plist, warnings []string) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", dir, err)}
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		p, err := read(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		plists = append(plists, p)
	}
	return plists, warnings
}

// Read parses one plist file.
func Read(path string) (Plist, error) {
	return read(path)
}

func read(path string) (Plist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Plist{}, err
	}
	p, err := Parse(data)
	p.Path = path
	return p, err
}

// Parse reads plist bytes (XML or binary).
func Parse(data []byte) (Plist, error) {
	var r raw
	if _, err := plist.Unmarshal(data, &r); err != nil {
		return Plist{}, err
	}
	if r.Label == "" {
		return Plist{}, errors.New("no Label")
	}
	args := r.ProgramArguments
	if r.Program != "" {
		args = append([]string{r.Program}, args...)
	}
	keepAlive := false
	switch k := r.KeepAlive.(type) {
	case bool:
		keepAlive = k
	case map[string]any:
		keepAlive = true // conditional KeepAlive still means "launchd keeps it up"
	}
	return Plist{
		Label: r.Label, Args: args,
		Schedule: schedule.Spec{
			Calendars: schedule.ParseCalendars(r.StartCalendarInterval),
			Interval:  r.StartInterval, RunAtLoad: r.RunAtLoad, KeepAlive: keepAlive,
		},
		StdoutPath: r.StandardOutPath, StderrPath: r.StandardErrorPath,
	}, nil
}
