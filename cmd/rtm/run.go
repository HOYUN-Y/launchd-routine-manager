package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// cmdRun is `rtm run <label> [--restart] [--yes] [--load] [--wait 10s]`. Confirmations are
// flags because the CLI is non-interactive.
func cmdRun(args []string, out, errw io.Writer, env sys.Env) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(errw)
	var o routine.RunOpts
	fs.BoolVar(&o.Restart, "restart", false, "restart the routine if it is running")
	fs.BoolVar(&o.Yes, "yes", false, "also confirm restarting a KeepAlive service")
	fs.BoolVar(&o.Load, "load", false, "register the plist with launchd first if it is not loaded")
	fs.DurationVar(&o.Wait, "wait", 10*time.Second, "how long to watch for the job to finish")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || o.Wait < 0 {
		return fmt.Errorf("usage: rtm run <label> [--restart] [--yes] [--load] [--wait 10s]")
	}
	r, err := lookup(pos[0], env)
	if err != nil {
		return err
	}

	res, err := routine.Run(env, r, o)
	if err != nil {
		if res.Bootstrapped {
			fmt.Fprintf(errw, "note: %s was registered with launchd (bootstrap) before this failed\n", r.Label)
		}
		return err
	}

	started := "started " + r.Label
	if res.Bootstrapped {
		started += " (registered with launchd first)"
	}
	fmt.Fprintln(out, started)
	st := res.Status
	switch {
	case !res.Finished:
		fmt.Fprintf(out, "no result after %s (state: %s, pid %d); check with: rtm show %s\n", o.Wait, st.State, st.PID, r.Label)
	default:
		fmt.Fprintf(out, "finished: exit %d (runs %d)\n", *st.LastExitCode, st.Runs)
	}
	for _, p := range r.LogPaths() {
		lines, _, err := tail(p, 5)
		if err != nil || len(lines) == 0 {
			continue
		}
		fmt.Fprintf(out, "log %s:\n", p)
		for _, l := range lines {
			fmt.Fprintln(out, "  | "+l)
		}
	}
	if res.Finished && *st.LastExitCode != 0 {
		return fmt.Errorf("%s exited with %d", r.Label, *st.LastExitCode)
	}
	return nil
}
