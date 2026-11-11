package routine

import (
	"errors"
	"fmt"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/launchd"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// KeepAlive reports whether the plist asks launchd to keep the job up (a service).
func (r Routine) KeepAlive() bool { return r.spec.KeepAlive }

type RunOpts struct {
	Restart bool          // restart a running job (kickstart -k)
	Yes     bool          // also required to restart a KeepAlive service
	Load    bool          // bootstrap first if the job is not loaded
	Wait    time.Duration // how long to watch for the job to finish
	Poll    time.Duration // 0 means 200ms
}

type RunResult struct {
	Bootstrapped bool // rtm registered the plist with launchd
	Finished     bool // the job ran and is no longer running
	Status       launchd.Status
}

// Run starts a routine through launchd (PLAN §5) so it runs in the same environment as a
// scheduled run. Policy lives here so the CLI and the TUI share it; confirmations are flags.
//
//	not loaded            -> refuse unless Load (then bootstrap)
//	running               -> refuse unless Restart; a KeepAlive service also needs Yes
//	otherwise             -> kickstart
//
// Afterwards it re-reads the state for up to Wait until the job has finished (see waitDone).
func Run(env sys.Env, r Routine, o RunOpts) (RunResult, error) {
	var res RunResult
	if r.Source == SourceCron {
		return res, fmt.Errorf("%s is a crontab entry; rtm run only starts launchd jobs", r.Label)
	}
	st := launchd.Query(env.Runner, env.UID, r.Label)
	res.Status = st
	baseline, kill := st.Runs, false

	switch {
	case !st.Loaded:
		if !o.Load {
			return res, fmt.Errorf("%s is not loaded; add --load to register it with launchctl bootstrap first", r.Label)
		}
		if err := launchd.Bootstrap(env.Runner, env.UID, r.Label, r.Path); err != nil {
			return res, err
		}
		res.Bootstrapped = true
		st = launchd.Query(env.Runner, env.UID, r.Label)
		res.Status = st
		if !st.Loaded {
			return res, errors.New("bootstrap succeeded but launchd does not list the job")
		}
		if st.Runs > 0 {
			// RunAtLoad/KeepAlive: launchd already started it. A kickstart now would be a no-op
			// or a needless second run, so just watch this one.
			return waitDone(env, r.Label, st.Runs-1, o, res)
		}
		baseline = st.Runs
	case st.State == "running":
		if !o.Restart {
			return res, fmt.Errorf("%s is already running (pid %d); use --restart to restart it", r.Label, st.PID)
		}
		if r.KeepAlive() && !o.Yes {
			return res, fmt.Errorf("%s is a KeepAlive service; restarting it needs --yes as well as --restart", r.Label)
		}
		kill = true
	}

	if err := launchd.Kickstart(env.Runner, env.UID, r.Label, kill); err != nil {
		return res, err
	}
	return waitDone(env, r.Label, baseline, o, res)
}

func waitDone(env sys.Env, label string, baseline int, o RunOpts, res RunResult) (RunResult, error) {
	poll := o.Poll
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	for i := 0; ; i++ {
		st := launchd.Query(env.Runner, env.UID, label)
		res.Status = st
		// Finished = no live process, one more run than before, and an exit code to report.
		// Real launchd passes through `state = xpcproxy` (pid set, "(never exited)") while the
		// job starts, and keeps the PREVIOUS run's exit code until the new one ends, so the state
		// text and the counter alone would report a run that is still in flight as finished.
		if st.Loaded && st.PID == 0 && st.State != "running" && st.Runs > baseline && st.LastExitCode != nil {
			res.Finished = true
			return res, nil
		}
		if time.Duration(i)*poll >= o.Wait {
			return res, nil
		}
		time.Sleep(poll)
	}
}
