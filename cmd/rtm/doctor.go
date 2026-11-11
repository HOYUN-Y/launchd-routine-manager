package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/doctor"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

func cmdDoctor(args []string, out, errw io.Writer, env sys.Env) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errw)
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	checks := doctor.Run(env)
	if *asJSON {
		if err := writeJSON(out, checks); err != nil {
			return err
		}
	} else {
		for _, c := range checks {
			fmt.Fprintf(out, "[%-4s] %-14s %s\n", c.Status, c.Name, c.Detail)
		}
	}
	if doctor.Failed(checks) {
		return errors.New("some checks failed")
	}
	return nil
}
