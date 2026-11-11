// Package schedule interprets plist schedule keys: a readable description and the next run time.
package schedule

import (
	"fmt"
	"strings"
	"time"
)

// Calendar is one StartCalendarInterval dict. nil means "any" (wildcard).
type Calendar struct {
	Minute, Hour, Day, Month, Weekday *int
}

// Spec is everything in a plist that decides when the job starts.
type Spec struct {
	Calendars []Calendar
	Interval  int // StartInterval, seconds
	RunAtLoad bool
	KeepAlive bool
}

var weekdays = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseCalendars accepts the decoded plist value: a dict, or an array of dicts.
// A value that is not an integer becomes -1, which matches nothing, instead of
// silently turning into a wildcard.
func ParseCalendars(v any) []Calendar {
	switch t := v.(type) {
	case map[string]any:
		return []Calendar{fromDict(t)}
	case []any:
		var out []Calendar
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, fromDict(m))
			}
		}
		return out
	}
	return nil
}

func fromDict(m map[string]any) Calendar {
	get := func(k string) *int {
		v, ok := m[k]
		if !ok {
			return nil
		}
		n := -1
		switch x := v.(type) {
		case int64:
			n = int(x)
		case uint64:
			n = int(x)
		case int:
			n = x
		}
		return &n
	}
	return Calendar{Minute: get("Minute"), Hour: get("Hour"), Day: get("Day"), Month: get("Month"), Weekday: get("Weekday")}
}

func (c Calendar) String() string {
	date := "daily"
	switch {
	case c.Month != nil || c.Day != nil:
		var p []string
		if c.Month != nil {
			p = append(p, fmt.Sprintf("month=%d", *c.Month))
		}
		if c.Day != nil {
			p = append(p, fmt.Sprintf("day=%d", *c.Day))
		}
		if c.Weekday != nil {
			p = append(p, fmt.Sprintf("weekday=%d", *c.Weekday))
		}
		date = strings.Join(p, " ")
	case c.Weekday != nil:
		date = "weekly " + weekdayName(*c.Weekday)
	}
	switch {
	case c.Hour != nil && c.Minute != nil:
		return fmt.Sprintf("%s %02d:%02d", date, *c.Hour, *c.Minute)
	case c.Hour != nil:
		return fmt.Sprintf("%s %02d:*", date, *c.Hour)
	case c.Minute != nil && date == "daily":
		return fmt.Sprintf("hourly :%02d", *c.Minute)
	case c.Minute != nil:
		return fmt.Sprintf("%s hourly :%02d", date, *c.Minute)
	case date == "daily":
		return "every minute"
	}
	return date + " every minute"
}

func weekdayName(n int) string {
	if n < 0 || n > 7 {
		return fmt.Sprintf("weekday=%d", n)
	}
	return weekdays[n%7] // launchd: 0 and 7 are both Sunday
}

// Describe is the short schedule text shown in `rtm list`.
func (s Spec) Describe() string {
	var parts []string
	for _, c := range s.Calendars {
		parts = append(parts, c.String())
	}
	if s.Interval > 0 {
		switch {
		case s.Interval%3600 == 0:
			parts = append(parts, fmt.Sprintf("every %dh", s.Interval/3600))
		case s.Interval%60 == 0:
			parts = append(parts, fmt.Sprintf("every %dm", s.Interval/60))
		default:
			parts = append(parts, fmt.Sprintf("every %ds", s.Interval))
		}
	}
	switch {
	case s.KeepAlive:
		parts = append(parts, "always")
	case s.RunAtLoad && len(parts) == 0:
		parts = append(parts, "at login")
	case s.RunAtLoad:
		parts = append(parts, "at load")
	}
	if len(parts) == 0 {
		return "manual"
	}
	return strings.Join(parts, ", ")
}

// horizonDays bounds the search; it covers a Feb 29 schedule across a leap-year gap.
const horizonDays = 366*4 + 1

// Next returns the earliest start strictly after now. lastRun (estimated from the log mtime,
// zero if unknown) is only used for StartInterval, which launchd counts from the previous
// start; a stale lastRun gives ok=false. RunAtLoad/KeepAlive alone have no next time either.
// shortcut: when both Day and Weekday are set they must both match (AND); verify against
// launchd before M4 edits such schedules.
func (s Spec) Next(now, lastRun time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	consider := func(t time.Time) {
		if !found || t.Before(best) {
			best, found = t, true
		}
	}
	for _, c := range s.Calendars {
		if t, ok := c.next(now); ok {
			consider(t)
		}
	}
	if s.Interval > 0 && !lastRun.IsZero() {
		// If last+interval is already past, the log mtime is not the real last start (the job
		// may run without writing logs), so the estimate is worthless: report "unknown".
		if t := lastRun.Add(time.Duration(s.Interval) * time.Second); t.After(now) {
			consider(t)
		}
	}
	return best, found
}

func (c Calendar) next(now time.Time) (time.Time, bool) {
	loc := now.Location()
	y, mo, d := now.Date()
	for off := 0; off < horizonDays; off++ {
		day := time.Date(y, mo, d+off, 0, 0, 0, 0, loc)
		if !c.dayMatches(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			if c.Hour != nil && *c.Hour != h {
				continue
			}
			for m := 0; m < 60; m++ {
				if c.Minute != nil && *c.Minute != m {
					continue
				}
				if t := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc); t.After(now) {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

func (c Calendar) dayMatches(day time.Time) bool {
	return (c.Month == nil || *c.Month == int(day.Month())) &&
		(c.Day == nil || *c.Day == day.Day()) &&
		(c.Weekday == nil || (*c.Weekday >= 0 && *c.Weekday <= 7 && *c.Weekday%7 == int(day.Weekday()))) // 7 == 0 == Sunday
}

// Relative formats t for humans: "14:00", "tomorrow 09:00", or "2026-11-20 09:00".
func Relative(t, now time.Time) string {
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	switch {
	case ty == ny && tm == nm && td == nd:
		return t.Format("15:04")
	case t.Format("2006-01-02") == now.AddDate(0, 0, 1).Format("2006-01-02"):
		return "tomorrow " + t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}
