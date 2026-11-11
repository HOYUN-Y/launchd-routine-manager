package launchd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
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

	// The schedule launchd holds in memory. It changes only when the job is (re)loaded, not when
	// the plist file changes, so it can differ from the file.
	Calendars   []schedule.Calendar `json:"-"`
	RunInterval int                 `json:"-"` // seconds, 0 if none
}

// IsRunning reports a live process. While a job starts, launchd shows `state = xpcproxy` with a
// pid, so the pid counts as much as `state = running`. run and schedule both decide with this.
func (s Status) IsRunning() bool { return s.Loaded && (s.State == "running" || s.PID != 0) }

// Query asks launchd about one service (launchctl print). It is read-only. Any failure
// (exit 113 "Could not find service", ...) means "not loaded".
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
		case "run interval": // "3600 seconds"
			st.RunInterval, _ = strconv.Atoi(strings.Fields(val + " ")[0])
		case "stdout path":
			st.StdoutPath = val
		case "stderr path":
			st.StderrPath = val
		}
	}
	st.Calendars = parseCalendars(text)
	return st
}

var descriptorLine = regexp.MustCompile(`^\s*"(\w+)" => (-?\d+)\s*$`)

// parseCalendars reads the StartCalendarInterval entries launchd holds. Each is an "event
// trigger" on the calendarinterval stream with a `descriptor = { "Hour" => 3 ... }` block.
func parseCalendars(text string) []schedule.Calendar {
	var out []schedule.Calendar
	calendar, inDescriptor := false, false
	vals := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case inDescriptor && t == "}":
			c := schedule.Calendar{}
			for k, v := range vals {
				v := v
				switch k {
				case "Minute":
					c.Minute = &v
				case "Hour":
					c.Hour = &v
				case "Day":
					c.Day = &v
				case "Month":
					c.Month = &v
				case "Weekday":
					c.Weekday = &v
				}
			}
			out = append(out, c)
			inDescriptor, calendar, vals = false, false, map[string]int{}
		case inDescriptor:
			if m := descriptorLine.FindStringSubmatch(line); m != nil {
				vals[m[1]], _ = strconv.Atoi(m[2])
			}
		case strings.HasPrefix(t, "stream = "):
			calendar = t == "stream = com.apple.launchd.calendarinterval"
		case calendar && t == "descriptor = {":
			inDescriptor = true
		}
	}
	return out
}
