// Package sys holds everything that touches the host: uid, home, external commands.
package sys

import (
	"bytes"
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

// Exec runs real commands.
type Exec struct{}

func (Exec) Run(name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
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
// error, so a test can't reach the real system by accident.
type FakeRunner struct {
	Results map[string]Result
	Calls   []string
}

func (f *FakeRunner) Run(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.Calls = append(f.Calls, key)
	r, ok := f.Results[key]
	if !ok {
		return "", fmt.Errorf("fake runner: unexpected command %q", key)
	}
	return r.Out, r.Err
}
