// Package sys holds everything that touches the host: uid, home, external commands.
package sys

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner is the single door to external commands (launchctl, plutil, crontab, zsh -lc).
// Tests swap it for FakeRunner so `go test` never touches the real launchd.
type Runner interface {
	// Run returns stdout. On failure the error carries stderr.
	Run(name string, args ...string) (string, error)
}

// Env is resolved at run time; nothing about the machine is hardcoded (PLAN §10).
type Env struct {
	Home            string
	UID             int
	SystemAgentsDir string // /Library/LaunchAgents; read-only for rtm
	Runner          Runner
	Now             func() time.Time // nil means time.Now; tests pin it
}

func (e Env) Clock() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

func Real() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{Home: home, UID: os.Getuid(), SystemAgentsDir: "/Library/LaunchAgents", Runner: Exec{}}, nil
}

// DefaultTimeout bounds one external command. A hung launchctl must not freeze a screen.
const DefaultTimeout = 15 * time.Second

// Exec runs real commands, each limited to Timeout (DefaultTimeout when zero).
//
// shortcut: a fixed time limit, no per-call cancellation. Esc and a wait deadline in the callers
// are only noticed between commands. Add a ctx to Runner.Run if a command must be cancellable.
type Exec struct{ Timeout time.Duration }

func (e Exec) Run(name string, args ...string) (string, error) {
	limit := e.Timeout
	if limit == 0 {
		limit = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second // a child that keeps the pipes open must not hold Wait past the kill
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %s (%w)", limit, err)
		}
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Result is a canned reply for FakeRunner.
type Result struct {
	Out string
	Err error
}

// FakeRunner replies from Results keyed by the full command line; any other command is an
// error, so a test can't reach the real system by accident. Queue holds replies for one
// command that change over time (e.g. `launchctl print` while a job starts and finishes):
// they are used in order, then Results takes over.
type FakeRunner struct {
	Results map[string]Result
	Queue   map[string][]Result
	Calls   []string
}

func (f *FakeRunner) Run(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.Calls = append(f.Calls, key)
	if q := f.Queue[key]; len(q) > 0 {
		f.Queue[key] = q[1:]
		return q[0].Out, q[0].Err
	}
	r, ok := f.Results[key]
	if !ok {
		return "", fmt.Errorf("fake runner: unexpected command %q", key)
	}
	return r.Out, r.Err
}
