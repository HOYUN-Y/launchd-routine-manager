// Package routine combines plist scan, crontab scan, classification and the tracked list.
package routine

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/classify"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/config"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/cron"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const (
	SourceUser   = "user_agent"   // ~/Library/LaunchAgents (editable in M4)
	SourceSystem = "system_agent" // /Library/LaunchAgents (read-only)
	SourceCron   = "cron"
)

// CronID identifies a crontab entry by what it does, not where it sits, so adding a line
// above it does not change its ID (and so does not change what `rtm track` points at).
// Two identical lines share one ID. The ID is stored in config.json: never change this.
func CronID(schedule, command string) string {
	sum := sha256.Sum256([]byte(schedule + "\x00" + command))
	return fmt.Sprintf("cron:%x", sum[:4])
}

// Routine is serialized as-is for --json.
type Routine struct {
	Label    string              `json:"label"`
	Source   string              `json:"source"`
	Path     string              `json:"path,omitempty"`     // plist path
	Line     int                 `json:"line,omitempty"`     // crontab line
	Schedule string              `json:"schedule,omitempty"` // plist: readable text; cron: the expression
	Command  []string            `json:"command"`
	AI       bool                `json:"ai"`
	Tracked  bool                `json:"tracked"`
	Evidence []classify.Evidence `json:"evidence"`

	// Filled by Enrich (plist routines only; crontab has no state).
	Status  *launchd.Status `json:"status,omitempty"`
	NextRun *time.Time      `json:"next_run,omitempty"`
	// NextRunEstimated: next_run is a guess (StartInterval counted from the log mtime).
	NextRunEstimated bool       `json:"next_run_estimated,omitempty"`
	LastRun          *time.Time `json:"last_run,omitempty"` // estimated from the log files' mtime
	StdoutPath       string     `json:"stdout_path,omitempty"`
	StderrPath       string     `json:"stderr_path,omitempty"`

	spec schedule.Spec
}

// Load is Discover with the tracked list read from config.json. A broken config is a warning
// (tracked items are then treated as absent); `track` and `untrack` refuse to write it.
func Load(env sys.Env) ([]Routine, []string) {
	tracked, err := config.Load(config.Path(env.Home))
	var warns []string
	if err != nil {
		warns = append(warns, "config: "+err.Error())
	}
	rs, w := Discover(env, tracked)
	return rs, append(warns, w...)
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
			out = append(out, Routine{
				Label: p.Label, Source: d.source, Path: p.Path, Command: p.Args,
				Schedule: p.Schedule.Describe(), spec: p.Schedule,
				StdoutPath: p.StdoutPath, StderrPath: p.StderrPath,
			})
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
				Label: CronID(e.Schedule, e.Command), Source: SourceCron, Line: e.Line,
				Schedule: e.Schedule, Command: []string{"/bin/sh", "-c", e.Command},
			})
		}
	}

	for i := range out {
		r := &out[i]
		file := r.Path
		if r.Source == SourceCron {
			file = "crontab"
		}
		r.Evidence = classify.Classify(file, r.Command, os.ReadFile)
		if r.Source == SourceCron {
			for j := range r.Evidence {
				if r.Evidence[j].Kind == "direct" {
					r.Evidence[j].Line = r.Line // plist evidence needs only the path; cron needs the line
				}
			}
		}
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

// Enrich asks launchd (read-only `launchctl print`) and fills status, log paths, last run and
// next run. Call it only for routines you are about to show.
func Enrich(env sys.Env, r *Routine) {
	if r.Source == SourceCron {
		return
	}
	st := launchd.Query(env.Runner, env.UID, r.Label)
	r.Status = &st
	if r.StdoutPath == "" {
		r.StdoutPath = st.StdoutPath
	}
	if r.StderrPath == "" {
		r.StderrPath = st.StderrPath
	}
	now := env.Clock()
	var last time.Time
	for _, p := range r.LogPaths() {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime().In(now.Location()) // one time zone in all output
		}
	}
	if !last.IsZero() {
		r.LastRun = &last
	}
	if t, est, ok := r.spec.Next(now, last); ok {
		r.NextRun, r.NextRunEstimated = &t, est
	}
}

// LogPaths returns the distinct log files (stdout and stderr are often the same file).
func (r Routine) LogPaths() []string {
	var out []string
	for _, p := range []string{r.StdoutPath, r.StderrPath} {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// StateText is the short state shown in `rtm list`.
func (r Routine) StateText() string {
	st := r.Status
	switch {
	case st == nil:
		return "-" // crontab: no state
	case !st.Loaded:
		return "not loaded"
	case st.State == "running":
		return "running"
	case st.LastExitCode != nil:
		return fmt.Sprintf("exit %d", *st.LastExitCode)
	}
	return "loaded"
}

// Find returns the routine with this label (user agents come before system agents).
func Find(rs []Routine, label string) (Routine, bool) {
	for _, r := range rs {
		if r.Label == label {
			return r, true
		}
	}
	return Routine{}, false
}
