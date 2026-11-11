package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/config"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/logs"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// Backend is everything the screens do to the outside world. Live is the real one; tests use a
// fake. There is no policy here or in the screens: Run, SetSchedule and RevertSchedule decide
// what needs a confirmation and say so with a *routine.ConfirmError.
type Backend interface {
	Load() ([]routine.Routine, []string)
	Run(ctx context.Context, r routine.Routine, o routine.RunOpts) (routine.RunResult, error)
	CanEditSchedule(r routine.Routine) error
	SetSchedule(ctx context.Context, r routine.Routine, e schedule.Edit, o routine.SchedOpts) (routine.SchedResult, error)
	RevertSchedule(ctx context.Context, r routine.Routine, o routine.SchedOpts) (routine.SchedResult, error)
	Track(label string) (changed bool, err error)
	Untrack(label string) (changed bool, err error)
	Logs(r routine.Routine, n int) (string, error)
}

// Live is the Backend on this Mac: the same internal functions the CLI uses.
type Live struct{ Env sys.Env }

func (b Live) Load() ([]routine.Routine, []string) {
	rs, warns := routine.Load(b.Env)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // one `launchctl print` each, a few at a time
	for i := range rs {
		wg.Add(1)
		sem <- struct{}{}
		go func(r *routine.Routine) {
			defer wg.Done()
			defer func() { <-sem }()
			routine.Enrich(b.Env, r)
		}(&rs[i])
	}
	wg.Wait()
	return rs, warns
}

func (b Live) Run(ctx context.Context, r routine.Routine, o routine.RunOpts) (routine.RunResult, error) {
	return routine.Run(ctx, b.Env, r, o)
}

func (b Live) CanEditSchedule(r routine.Routine) error { return routine.CanEditSchedule(b.Env, r) }

func (b Live) SetSchedule(ctx context.Context, r routine.Routine, e schedule.Edit, o routine.SchedOpts) (routine.SchedResult, error) {
	return routine.SetSchedule(ctx, b.Env, r, e, o)
}

func (b Live) RevertSchedule(ctx context.Context, r routine.Routine, o routine.SchedOpts) (routine.SchedResult, error) {
	return routine.RevertSchedule(ctx, b.Env, r, o)
}

func (b Live) Track(label string) (bool, error) { return config.Track(config.Path(b.Env.Home), label) }
func (b Live) Untrack(label string) (bool, error) {
	return config.Untrack(config.Path(b.Env.Home), label)
}

// Logs returns the last n lines of each log file, with a header per file when there are two.
func (b Live) Logs(r routine.Routine, n int) (string, error) {
	paths := r.LogPaths()
	if len(paths) == 0 {
		return "", fmt.Errorf("%s has no log path (no StandardOutPath/StandardErrorPath)", r.Label)
	}
	var out []string
	for _, p := range paths {
		if len(paths) > 1 {
			out = append(out, "==> "+p+" <==")
		}
		lines, _, err := logs.Tail(p, n)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out = append(out, "(log file does not exist yet: "+p+")")
		case err != nil:
			return "", err
		default:
			out = append(out, lines...)
		}
	}
	return strings.Join(out, "\n"), nil
}
