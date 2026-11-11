package launchd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// Status is what `launchctl print gui/<uid>/<label>` says about a service.
type Status struct {
	Loaded       bool   `json:"loaded"`
	State        string `json:"state,omitempty"` // running, not running, spawn scheduled, ...
	PID          int    `json:"pid,omitempty"`
	Runs         int    `json:"runs"`
	LastExitCode *int   `json:"last_exit_code,omitempty"` // nil: never exited
	StdoutPath   string `json:"stdout_path,omitempty"`
	StderrPath   string `json:"stderr_path,omitempty"`
}

// Query asks launchd about one service. This is the only launchctl call M2 makes, and it is
// read-only. Any failure (exit 113 "Could not find service", ...) means "not loaded".
func Query(r sys.Runner, uid int, label string) Status {
	out, err := r.Run("launchctl", "print", fmt.Sprintf("gui/%d/%s", uid, label))
	if err != nil {
		return Status{}
	}
	return ParsePrint(out)
}

// ParsePrint reads the service's own keys: lines indented by exactly one tab. Deeper lines
// belong to nested blocks (environment, endpoints, ...) and may reuse key names.
func ParsePrint(text string) Status {
	st := Status{Loaded: true}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "\t"), " = ")
		if !ok {
			continue
		}
		switch key {
		case "state":
			st.State = val
		case "pid":
			st.PID, _ = strconv.Atoi(val)
		case "runs":
			st.Runs, _ = strconv.Atoi(val)
		case "last exit code", "last exit status": // second spelling: older macOS, not verified here
			// "0", "1", "78: EX_CONFIG", "(never exited)"
			if n, err := strconv.Atoi(strings.SplitN(val, ":", 2)[0]); err == nil {
				st.LastExitCode = &n
			}
		case "stdout path":
			st.StdoutPath = val
		case "stderr path":
			st.StderrPath = val
		}
	}
	return st
}
