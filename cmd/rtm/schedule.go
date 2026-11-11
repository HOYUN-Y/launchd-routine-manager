package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const scheduleUsage = `usage: rtm schedule <label> (--daily HH:MM | --weekly mon,wed HH:MM | --at HH:MM,HH:MM | --every 30m | --revert) [--yes] [--force]`

// cmdSchedule is `rtm schedule`. Confirmations are flags because the CLI is non-interactive:
// --yes allows reloading a running job, --force allows replacing a schedule rtm cannot express.
func cmdSchedule(ctx context.Context, args []string, out, errw io.Writer, env sys.Env) error {
	fs := flag.NewFlagSet("schedule", flag.ContinueOnError)
	fs.SetOutput(errw)
	daily := fs.String("daily", "", "run every day at HH:MM")
	weekly := fs.String("weekly", "", "run on these weekdays (mon,wed); the time follows as HH:MM")
	at := fs.String("at", "", "run every day at each HH:MM,HH:MM")
	every := fs.String("every", "", "run every N minutes/hours, e.g. 30m, 2h")
	revert := fs.Bool("revert", false, "restore the newest backup")
	var o routine.SchedOpts
	fs.BoolVar(&o.Yes, "yes", false, "allow reloading a running job (it is stopped)")
	fs.BoolVar(&o.Force, "force", false, "replace a schedule rtm cannot express")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}

	modes := 0
	for _, set := range []bool{*daily != "", *weekly != "", *at != "", *every != "", *revert} {
		if set {
			modes++
		}
	}
	wantPos := 1
	if *weekly != "" {
		wantPos = 2 // <label> and the time that follows the weekdays
	}
	if modes != 1 || len(pos) != wantPos {
		return fmt.Errorf("%s", scheduleUsage)
	}

	var edit schedule.Edit
	switch {
	case *daily != "":
		edit, err = schedule.Daily(*daily)
	case *weekly != "":
		edit, err = schedule.Weekly(*weekly, pos[1])
	case *at != "":
		edit, err = schedule.At(*at)
	case *every != "":
		edit, err = schedule.Every(*every)
	}
	if err != nil {
		return err
	}

	r, err := lookup(pos[0], env)
	if err != nil {
		return err
	}
	var res routine.SchedResult
	if *revert {
		res, err = routine.RevertSchedule(ctx, env, r, o)
	} else {
		res, err = routine.SetSchedule(ctx, env, r, edit, o)
	}
	if res.BackupPath != "" && err != nil {
		fmt.Fprintf(errw, "note: backup of the previous plist: %s\n", res.BackupPath)
	}
	if err != nil {
		return err
	}

	if res.Unchanged {
		fmt.Fprintf(out, "%s already has this schedule (%s); nothing changed\n", r.Label, res.After)
		return nil
	}
	fmt.Fprintf(out, "%s: %s -> %s\n", r.Label, res.Before, res.After)
	if *revert {
		fmt.Fprintf(out, "restored the newest backup; the plist it replaced is kept at %s\n", res.BackupPath)
	} else {
		fmt.Fprintf(out, "backup: %s\n", res.BackupPath)
	}
	if res.Reloaded {
		fmt.Fprintln(out, "reloaded: launchctl bootout + bootstrap")
	}
	fmt.Fprintf(out, "verified: %s\n", res.Verified)
	for _, n := range res.Notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
	return nil
}
