package routine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

type SchedOpts struct {
	Yes   bool // reloading stops a running job
	Force bool // overwrite a schedule rtm cannot express
}

type SchedResult struct {
	Unchanged  bool   // already what was asked: nothing was touched
	Before     string // schedule text before
	After      string // schedule text after
	BackupPath string // previous plist; for a revert, the copy of the plist that was replaced
	Reloaded   bool   // bootout + bootstrap were done
	Verified   string // how the new schedule was confirmed
	Notes      []string
}

// SetSchedule changes a routine's schedule (PLAN §6). Order: refuse early, back up, write only
// the schedule keys, plutil -lint, bootout + bootstrap, then read launchd's state to confirm.
// Every step after the write undoes the write when it fails.
func SetSchedule(ctx context.Context, env sys.Env, r Routine, e schedule.Edit, o SchedOpts) (SchedResult, error) {
	if err := ctx.Err(); err != nil {
		return SchedResult{}, err
	}
	if err := editable(env, r); err != nil {
		return SchedResult{}, err
	}
	orig, err := os.ReadFile(r.Path)
	if err != nil {
		return SchedResult{}, err
	}
	cur, err := launchd.Read(r.Path)
	if err != nil {
		return SchedResult{}, err
	}
	// After keeps RunAtLoad/KeepAlive: they stay in the plist and are part of what the job does.
	resulting := schedule.Spec{Calendars: e.Calendars, Interval: e.Interval, RunAtLoad: cur.Schedule.RunAtLoad, KeepAlive: cur.Schedule.KeepAlive}
	res := SchedResult{Before: cur.Schedule.Describe(), After: resulting.Describe()}
	if cur.Schedule.Interval == e.Interval && schedule.SameCalendars(cur.Schedule.Calendars, e.Calendars) {
		res.Unchanged = true
		return res, nil
	}
	if !cur.Schedule.Expressible() && !o.Force {
		return res, &ConfirmError{NeedForce, fmt.Sprintf("the current schedule of %s cannot be expressed by rtm: %s\n"+
			"overwriting it would lose that; add --force to replace it (the old plist is backed up)", r.Label, res.Before)}
	}
	updated, err := launchd.ReplaceSchedule(orig, e)
	if err != nil {
		return res, err
	}
	return apply(ctx, env, r, orig, updated, e.Spec(), &res, o, "")
}

// RevertSchedule restores the newest backup made by SetSchedule. The plist it replaces is kept
// as <label>-<time>-pre-revert.plist in the same directory, so a revert cannot lose hand edits.
func RevertSchedule(ctx context.Context, env sys.Env, r Routine, o SchedOpts) (SchedResult, error) {
	if err := ctx.Err(); err != nil {
		return SchedResult{}, err
	}
	if err := editable(env, r); err != nil {
		return SchedResult{}, err
	}
	bpath, err := launchd.LatestBackup(env.Home, r.Label)
	if err != nil {
		return SchedResult{}, err
	}
	backup, err := os.ReadFile(bpath)
	if err != nil {
		return SchedResult{}, err
	}
	want, err := parseSpec(backup)
	if err != nil {
		return SchedResult{}, fmt.Errorf("backup %s is not a usable plist: %w", bpath, err)
	}
	orig, err := os.ReadFile(r.Path)
	if err != nil {
		return SchedResult{}, err
	}
	cur, _ := parseSpec(orig)
	res := SchedResult{Before: cur.Describe(), After: want.Describe()}
	if bytes.Equal(orig, backup) {
		res.Unchanged = true
		return res, nil
	}
	return apply(ctx, env, r, orig, backup, want, &res, o, "pre-revert")
}

// apply is the shared tail: save orig, write updated, lint, reload, confirm that launchd (or,
// for a job that is not loaded, the file) holds want.
func apply(ctx context.Context, env sys.Env, r Routine, orig, updated []byte, want schedule.Spec, res *SchedResult, o SchedOpts, backupSuffix string) (SchedResult, error) {
	st := launchd.Query(env.Runner, env.UID, r.Label)
	if st.IsRunning() && !o.Yes {
		return *res, &ConfirmError{NeedReloadRunning, fmt.Sprintf("%s is running (pid %d); reloading it for the new schedule would stop it. Add --yes to go ahead", r.Label, st.PID)}
	}

	bpath, err := launchd.SaveBackup(env.Home, r.Label, backupSuffix, env.Clock(), orig)
	if err != nil {
		return *res, fmt.Errorf("cannot save a backup, nothing was changed: %w", err)
	}
	res.BackupPath = bpath

	restore := func() error { return launchd.WritePlist(r.Label, r.Path, orig) }
	fail := func(cause error) (SchedResult, error) {
		if rerr := restore(); rerr != nil {
			return *res, fmt.Errorf("%w; ALSO the plist could not be restored (%v): copy %s back to %s by hand", cause, rerr, bpath, r.Path)
		}
		return *res, fmt.Errorf("%w (the plist was restored; backup kept at %s)", cause, bpath)
	}

	if err := launchd.WritePlist(r.Label, r.Path, updated); err != nil {
		return *res, fmt.Errorf("writing the plist: %w", err)
	}
	if err := launchd.Lint(env.Runner, r.Path); err != nil {
		return fail(fmt.Errorf("plutil -lint rejected the new plist: %w", err))
	}

	if st.Loaded {
		if err := launchd.Bootout(env.Runner, env.UID, r.Label); err != nil {
			return fail(fmt.Errorf("launchctl bootout failed, the job keeps its old schedule: %w", err))
		}
		if err := launchd.Bootstrap(env.Runner, env.UID, r.Label, r.Path); err != nil {
			// The job is now unloaded. Put the old plist back and load that, so it is not left dead.
			cause := fmt.Errorf("launchctl bootstrap failed after bootout: %w", err)
			if rerr := restore(); rerr != nil {
				return *res, fmt.Errorf("%w; ALSO the plist could not be restored (%v): the job is NOT loaded; copy %s back to %s and run launchctl bootstrap", cause, rerr, bpath, r.Path)
			}
			if berr := launchd.Bootstrap(env.Runner, env.UID, r.Label, r.Path); berr != nil {
				return *res, fmt.Errorf("%w; the old plist was restored but loading it failed too (%v): the job is NOT loaded", cause, berr)
			}
			return *res, fmt.Errorf("%w (the old plist was restored and loaded again)", cause)
		}
		res.Reloaded = true
		if r.spec.RunAtLoad || r.spec.KeepAlive {
			res.Notes = append(res.Notes, "RunAtLoad/KeepAlive is set: launchd starts the job when it is loaded, so the reload may have started it")
		}
	}

	// confirm
	if res.Reloaded {
		// A real one-second deadline (not a count of sleeps), and ctx can cut it short. The change
		// itself is never interrupted halfway: only this confirmation wait is.
		var last launchd.Status
		deadline := time.Now().Add(time.Second)
		for {
			last = launchd.Query(env.Runner, env.UID, r.Label)
			if last.Loaded && last.RunInterval == want.Interval && schedule.SameCalendars(last.Calendars, want.Calendars) {
				res.Verified = "launchd now holds " + res.After
				return *res, nil
			}
			if !time.Now().Before(deadline) {
				break
			}
			if !sleep(ctx, 100*time.Millisecond) {
				return *res, fmt.Errorf("interrupted before launchd confirmed the new schedule (the change was made; backup at %s); check with: rtm show %s", bpath, r.Label)
			}
		}
		return *res, fmt.Errorf("reloaded, but launchctl print does not show the new schedule (loaded=%v, interval=%d, %d calendar entries); "+
			"backup at %s, undo with: rtm schedule %s --revert", last.Loaded, last.RunInterval, len(last.Calendars), bpath, r.Label)
	}
	got, err := launchd.Read(r.Path)
	if err != nil || got.Schedule.Interval != want.Interval || !schedule.SameCalendars(got.Schedule.Calendars, want.Calendars) {
		return fail(fmt.Errorf("the plist on disk does not hold the new schedule after writing (read error: %v)", err))
	}
	res.Verified = "plist file holds " + res.After + "; the job is not loaded in launchd, so nothing was reloaded"
	return *res, nil
}

// editable refuses everything rtm must not write: system agents, crontab, plists outside
// ~/Library/LaunchAgents, symlinks (a rename would replace the link), and, via the dev guard,
// labels that are not test labels. It runs before anything is touched.
func editable(env sys.Env, r Routine) error {
	if err := launchd.CheckLabel(r.Label); err != nil {
		return err
	}
	switch r.Source {
	case SourceCron:
		return fmt.Errorf("%s is a crontab entry; rtm schedule only edits launchd plists", r.Label)
	case SourceSystem:
		return fmt.Errorf("%s is in /Library/LaunchAgents, which rtm only reads (changing it needs administrator rights)", r.Label)
	}
	agents := filepath.Join(env.Home, "Library", "LaunchAgents")
	if filepath.Dir(r.Path) != agents {
		return fmt.Errorf("%s is not in %s; rtm only edits plists there", r.Path, agents)
	}
	fi, err := os.Lstat(r.Path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New(r.Path + " is a symlink; writing would replace the link. Edit the file it points to by hand")
	}
	return nil
}

func parseSpec(data []byte) (schedule.Spec, error) {
	p, err := launchd.Parse(data)
	return p.Schedule, err
}
