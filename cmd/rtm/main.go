// rtm: list, track and (in later milestones) run and reschedule launchd routines.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"text/tabwriter"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/config"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const usage = `usage: rtm <command>

  list [--all] [--json]          list AI routines (--all: every plist and crontab entry)
  show <label> [--json]          details: schedule, next run, state, logs, command
  run <label> [--restart] [--yes] [--load] [--wait 10s]
                                 start a routine now via launchctl kickstart
  logs <label> [-n 50] [-f]      last lines of the routine's log files
  track <label>                  mark a routine as AI by hand
  untrack <label>                undo track
  doctor [--json]                check claude/codex paths, LaunchAgents access, launchctl
`

func main() {
	env, err := sys.Real()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rtm:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, env)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, out, errw io.Writer, env sys.Env) int {
	if len(args) == 0 {
		fmt.Fprint(errw, usage) // the TUI arrives in M5
		return 2
	}
	var err error
	switch args[0] {
	case "list":
		err = cmdList(args[1:], out, errw, env)
	case "show":
		err = cmdShow(args[1:], out, errw, env)
	case "run":
		err = cmdRun(args[1:], out, errw, env)
	case "logs":
		err = cmdLogs(ctx, args[1:], out, errw, env)
	case "doctor":
		err = cmdDoctor(args[1:], out, errw, env)
	case "track", "untrack":
		err = cmdTrack(args[0] == "track", args[1:], out, errw, env)
	default:
		fmt.Fprintf(errw, "rtm: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(errw, "rtm:", err)
		return 1
	}
	return 0
}

// discover loads the tracked list and scans. Warnings (unreadable plists, a broken config,
// crontab trouble) are returned, not printed: only `list` and `doctor` show them, so
// `show` and `logs` stay quiet about unrelated files. A broken config.json is a warning here
// (tracked items are treated as absent); `track` and `untrack` refuse to write it.
func discover(env sys.Env) ([]routine.Routine, []string) {
	tracked, err := config.Load(config.Path(env.Home))
	var warns []string
	if err != nil {
		warns = append(warns, "config: "+err.Error())
	}
	rs, w := routine.Discover(env, tracked)
	return rs, append(warns, w...)
}

// parse lets flags come before or after positional args: `rtm show <label> --json`.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep `>>` readable in commands
	return enc.Encode(v)
}

func cmdList(args []string, out, errw io.Writer, env sys.Env) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(errw)
	all := fs.Bool("all", false, "include non-AI entries")
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rs, warns := discover(env)
	for _, w := range warns {
		fmt.Fprintln(errw, "warning:", w)
	}
	shown := make([]routine.Routine, 0, len(rs)) // non-nil: "[]" not "null"
	for _, r := range rs {
		if *all || r.AI {
			shown = append(shown, r)
		}
	}
	for i := range shown {
		routine.Enrich(env, &shown[i])
	}
	if *asJSON {
		return writeJSON(out, shown)
	}
	if len(shown) == 0 {
		fmt.Fprintln(out, "no AI routines found (use --all to see everything)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tSOURCE\tCLASS\tSCHEDULE\tSTATE\tNEXT\tEVIDENCE")
	now := env.Clock()
	for _, r := range shown {
		class, evidence := "-", ""
		if r.AI {
			class, evidence = "ai", r.Evidence[0].String()
		}
		if r.Tracked {
			class = "ai (tracked)"
		}
		next := "-"
		if r.NextRun != nil {
			next = schedule.Relative(*r.NextRun, now)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Label, r.Source, class, r.Schedule, r.StateText(), next, evidence)
	}
	return tw.Flush()
}

func cmdTrack(add bool, args []string, out, errw io.Writer, env sys.Env) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: rtm track|untrack <label>")
	}
	label, path := args[0], config.Path(env.Home)
	if !add {
		changed, err := config.Untrack(path, label) // allowed for unknown labels so stale entries can be cleared
		if err != nil {
			return err
		}
		if !changed {
			fmt.Fprintf(out, "%s was not tracked\n", label)
			return nil
		}
		fmt.Fprintf(out, "untracked %s\n", label)
		return nil
	}
	rs, _ := discover(env)
	found := false
	for _, r := range rs {
		found = found || r.Label == label
	}
	if !found {
		return fmt.Errorf("unknown label %q (see `rtm list --all`)", label)
	}
	changed, err := config.Track(path, label)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(out, "%s is already tracked\n", label)
		return nil
	}
	fmt.Fprintf(out, "tracking %s\n", label)
	return nil
}
