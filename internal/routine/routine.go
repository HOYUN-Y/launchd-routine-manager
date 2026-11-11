// Package routine combines plist scan, crontab scan, classification and the tracked list.
package routine

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/classify"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/cron"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const (
	SourceUser   = "user_agent"   // ~/Library/LaunchAgents (editable in M4)
	SourceSystem = "system_agent" // /Library/LaunchAgents (read-only)
	SourceCron   = "cron"
)

// Routine is serialized as-is for --json.
type Routine struct {
	Label    string              `json:"label"`
	Source   string              `json:"source"`
	Path     string              `json:"path,omitempty"`     // plist path
	Line     int                 `json:"line,omitempty"`     // crontab line
	Schedule string              `json:"schedule,omitempty"` // cron only until M2
	Command  []string            `json:"command"`
	AI       bool                `json:"ai"`
	Tracked  bool                `json:"tracked"`
	Evidence []classify.Evidence `json:"evidence"`
}

// Discover scans everything and returns routines sorted by label, plus non-fatal warnings.
func Discover(env sys.Env, tracked []string) ([]Routine, []string) {
	var out []Routine
	var warns []string

	for _, d := range []struct{ dir, source string }{
		{filepath.Join(env.Home, "Library", "LaunchAgents"), SourceUser},
		{env.SystemAgentsDir, SourceSystem},
	} {
		plists, w := launchd.Scan(d.dir)
		warns = append(warns, w...)
		for _, p := range plists {
			out = append(out, Routine{Label: p.Label, Source: d.source, Path: p.Path, Command: p.Args})
		}
	}

	text, err := env.Runner.Run("crontab", "-l")
	switch {
	case err != nil && strings.Contains(err.Error(), "no crontab"):
	case err != nil:
		warns = append(warns, fmt.Sprintf("crontab -l: %v", err))
	default:
		entries, bad := cron.Parse(text)
		for _, n := range bad {
			warns = append(warns, fmt.Sprintf("crontab line %d: cannot parse", n))
		}
		for _, e := range entries {
			out = append(out, Routine{
				Label: fmt.Sprintf("cron:%d", e.Line), Source: SourceCron, Line: e.Line,
				Schedule: e.Schedule, Command: []string{"/bin/sh", "-c", e.Command},
			})
		}
	}

	for i := range out {
		r := &out[i]
		r.Evidence = classify.Classify(r.Command, os.ReadFile)
		if slices.Contains(tracked, r.Label) {
			r.Tracked = true
			r.Evidence = append(r.Evidence, classify.Evidence{Kind: "tracked"})
		}
		r.AI = len(r.Evidence) > 0
		if r.Evidence == nil { // [] not null in JSON
			r.Evidence = []classify.Evidence{}
		}
		if r.Command == nil {
			r.Command = []string{}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, warns
}
