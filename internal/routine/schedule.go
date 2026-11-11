package routine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	reload := st.Loaded
	if reload {
		// Ask again, right before bootout: the job may have started while we wrote and linted. A
		// bootout would stop it, so without --yes put the file back and ask.
		cur := launchd.Query(env.Runner, env.UID, r.Label)
		if cur.IsRunning() && !o.Yes {
			return fail(&ConfirmError{NeedReloadRunning, fmt.Sprintf("%s started running (pid %d) while the change was being prepared; reloading it would stop it. Add --yes to go ahead", r.Label, cur.PID)})
		}
		reload = cur.Loaded // it may have been unloaded meanwhile: then there is nothing to reload
	}
	if reload {
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
		return undoReload(env, r, orig, bpath, res, restore, fmt.Errorf(
			"reloaded, but launchctl print does not show the new schedule (loaded=%v, interval=%d, %d calendar entries)",
			last.Loaded, last.RunInterval, len(last.Calendars)))
	}
	got, err := launchd.Read(r.Path)
	if err != nil || got.Schedule.Interval != want.Interval || !schedule.SameCalendars(got.Schedule.Calendars, want.Calendars) {
		return fail(fmt.Errorf("the plist on disk does not hold the new schedule after writing (read error: %v)", err))
	}
	res.Verified = "plist file holds " + res.After + "; the job is not loaded in launchd, so nothing was reloaded"
	return *res, nil
}

// CanEditSchedule says whether SetSchedule may touch this routine at all (the refusals below),
// so a UI can say no before it shows a form.
func CanEditSchedule(env sys.Env, r Routine) error { return editable(env, r) }

// editable refuses everything rtm must not write: system agents, crontab, plists outside
// ~/Library/LaunchAgents, symlinks (a rename would replace the link), and, via the dev guard,
// labels that are not test labels. It runs before anything is touched.
func editable(env sys.Env, r Routine) error {
	if err := launchd.CheckLabel(r.Label); err != nil {
		return err
	}
	// the backup is named after the Label: a Label that is not a plain file name stops everything
	if err := launchd.CheckFileLabel(r.Label); err != nil {
		return fmt.Errorf("%w; refusing to change it", err)
	}
	switch r.Source {
	case SourceCron:
		return fmt.Errorf("%s is a crontab entry; rtm schedule only edits launchd plists", r.Label)
	case SourceSystem:
		return fmt.Errorf("%s is in /Library/LaunchAgents, which rtm only reads (changing it needs administrator rights)", r.Label)
	}
	// Compare REAL directories. ~/Library/LaunchAgents (or a parent) may itself be a symlink, which is
	// fine (dotfiles setups) unless it leads into /Library or /System; and the plist's directory has
	// to resolve to that same place, whatever path it was reached by.
	// shortcut: a check, then a write: a link swapped in between the two is not guarded against.
	agents := filepath.Join(env.Home, "Library", "LaunchAgents")
	realAgents, err := filepath.EvalSymlinks(agents)
	if err != nil {
		return fmt.Errorf("cannot resolve %s: %w", agents, err)
	}
	if underSystem(realAgents) {
		return fmt.Errorf("%s resolves to %s, a system location; rtm only reads there", agents, realAgents)
	}
	realDir, err := filepath.EvalSymlinks(filepath.Dir(r.Path))
	if err != nil {
		return fmt.Errorf("cannot resolve the directory of %s: %w", r.Path, err)
	}
	if realDir != realAgents {
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

// underSystem reports whether a resolved path is inside /Library or /System, which rtm never writes.
func underSystem(p string) bool {
	p = filepath.Clean(p)
	for _, root := range []string{"/Library", "/System"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// undoReload runs after a reload that launchd does not confirm: put the original plist back and
// load it again, then say exactly what state things are in, including every step that failed.
func undoReload(env sys.Env, r Routine, orig []byte, bpath string, res *SchedResult, restore func() error, cause error) (SchedResult, error) {
	if err := restore(); err != nil {
		return *res, fmt.Errorf("%w; ALSO the original plist could not be restored (%v): launchd still holds the new schedule; copy %s back to %s by hand and reload it (launchctl bootout, then bootstrap)", cause, err, bpath, r.Path)
	}
	if err := launchd.Bootout(env.Runner, env.UID, r.Label); err != nil {
		return *res, fmt.Errorf("%w; the original plist was restored but launchctl bootout failed (%v): launchd still holds the new schedule; reload it by hand", cause, err)
	}
	if err := launchd.Bootstrap(env.Runner, env.UID, r.Label, r.Path); err != nil {
		return *res, fmt.Errorf("%w; the original plist was restored but loading it failed (%v): the job is NOT loaded; run launchctl bootstrap gui/%d %s", cause, err, env.UID, r.Path)
	}
	res.Reloaded = true
	if want, err := parseSpec(orig); err == nil {
		got := launchd.Query(env.Runner, env.UID, r.Label)
		if got.Loaded && got.RunInterval == want.Interval && schedule.SameCalendars(got.Calendars, want.Calendars) {
			return *res, fmt.Errorf("%w; the original plist was restored and loaded again; launchd holds the original schedule again (backup kept at %s)", cause, bpath)
		}
	}
	return *res, fmt.Errorf("%w; the original plist was restored and loaded again, but launchctl print does not show the original schedule either: check with rtm show %s (backup kept at %s)", cause, r.Label, bpath)
}
