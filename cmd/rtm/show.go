package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// lookup finds one routine by label and fills in its live state.
func lookup(label string, env sys.Env) (routine.Routine, error) {
	rs, _ := discover(env)
	r, ok := routine.Find(rs, label)
	if !ok {
		return r, fmt.Errorf("unknown label %q (see `rtm list --all`)", label)
	}
	routine.Enrich(env, &r)
	return r, nil
}

func cmdShow(args []string, out, errw io.Writer, env sys.Env) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(errw)
	asJSON := fs.Bool("json", false, "JSON output")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: rtm show <label> [--json]")
	}
	r, err := lookup(pos[0], env)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, r)
	}

	row := func(k, v string) { fmt.Fprintf(out, "%-11s %s\n", k+":", v) }
	when := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.Format("2006-01-02 15:04:05")
	}
	row("label", r.Label)
	src := r.Source
	switch {
	case r.Path != "":
		src += " (" + r.Path + ")"
	case r.Line > 0:
		src += fmt.Sprintf(" (crontab line %d)", r.Line)
	}
	row("source", src)
	class := "not AI"
	if r.AI {
		class = "AI"
	}
	row("class", class)
	for _, e := range r.Evidence {
		row("", "- "+e.String())
	}
	row("schedule", r.Schedule)
	row("next run", when(r.NextRun))
	if st := r.Status; st == nil {
		row("state", "n/a (crontab keeps no state)")
	} else if !st.Loaded {
		row("state", "not loaded")
	} else {
		state := st.State
		if st.PID > 0 {
			state += fmt.Sprintf(" (pid %d)", st.PID)
		}
		row("state", state)
		row("runs", fmt.Sprint(st.Runs))
		exit := "never exited"
		if st.LastExitCode != nil {
			exit = fmt.Sprint(*st.LastExitCode)
		}
		row("last exit", exit)
	}
	row("last run", when(r.LastRun)+" (estimated from log mtime)")
	if r.StdoutPath == "" && r.StderrPath == "" {
		row("logs", "none configured")
	} else {
		row("stdout", r.StdoutPath)
		row("stderr", r.StderrPath)
	}
	row("command", strings.Join(r.Command, " "))
	return nil
}
