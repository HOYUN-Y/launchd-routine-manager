package schedule

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Edit is a new schedule to write into a plist: calendar entries or a StartInterval, never both.
type Edit struct {
	Calendars []Calendar
	Interval  int // seconds
}

// Spec is the Edit as it will read back from the plist (RunAtLoad/KeepAlive are not touched).
func (e Edit) Spec() Spec { return Spec{Calendars: e.Calendars, Interval: e.Interval} }

func ip(n int) *int { return &n }

// ParseTime reads "9:05" or "09:05".
func ParseTime(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 || len(m) != 2 {
		return 0, 0, fmt.Errorf("bad time %q, want HH:MM (00:00-23:59)", s)
	}
	return hour, minute, nil
}

// Daily is "--daily 09:00".
func Daily(t string) (Edit, error) { return At(t) }

// At is "--at 09:00,18:00": every day at each time.
func At(times string) (Edit, error) {
	var e Edit
	seen := map[string]bool{}
	for _, t := range strings.Split(times, ",") {
		h, m, err := ParseTime(t)
		if err != nil {
			return Edit{}, err
		}
		if k := fmt.Sprintf("%02d:%02d", h, m); !seen[k] {
			seen[k] = true
			e.Calendars = append(e.Calendars, Calendar{Hour: ip(h), Minute: ip(m)})
		}
	}
	return e, nil
}

// Weekly is "--weekly mon,wed 09:00": one calendar entry per day, all at the same time.
func Weekly(days, t string) (Edit, error) {
	h, m, err := ParseTime(t)
	if err != nil {
		return Edit{}, err
	}
	var e Edit
	seen := map[int]bool{}
	for _, d := range strings.Split(days, ",") {
		wd := -1
		for i, name := range weekdays {
			if strings.EqualFold(strings.TrimSpace(d), name) {
				wd = i
			}
		}
		if wd < 0 {
			return Edit{}, fmt.Errorf("bad weekday %q, want sun,mon,tue,wed,thu,fri,sat", d)
		}
		if !seen[wd] {
			seen[wd] = true
			e.Calendars = append(e.Calendars, Calendar{Weekday: ip(wd), Hour: ip(h), Minute: ip(m)})
		}
	}
	return e, nil
}

// Every is "--every 30m" (also 2h, 90s...). The minimum is one minute.
func Every(s string) (Edit, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d%time.Second != 0 {
		return Edit{}, fmt.Errorf("bad interval %q, want e.g. 30m or 2h", s)
	}
	if d < time.Minute {
		return Edit{}, fmt.Errorf("interval %q is below the 1m minimum", s)
	}
	return Edit{Interval: int(d / time.Second)}, nil
}

// Expressible reports whether rtm can show this schedule in its own terms (PLAN §6), i.e. whether
// overwriting it loses nothing rtm could have written back: nothing, an interval alone, daily/at
// times, or weekdays that all share one time. Month/Day entries, hourly entries, a calendar
// mixed with an interval, or out-of-range values are not expressible.
func (s Spec) Expressible() bool {
	if len(s.Calendars) == 0 {
		return true
	}
	if s.Interval > 0 {
		return false
	}
	weekly, daily := 0, 0
	var first *Calendar
	for i, c := range s.Calendars {
		if c.Month != nil || c.Day != nil || c.Hour == nil || c.Minute == nil ||
			*c.Hour < 0 || *c.Hour > 23 || *c.Minute < 0 || *c.Minute > 59 {
			return false
		}
		if c.Weekday == nil {
			daily++
			continue
		}
		if *c.Weekday < 0 || *c.Weekday > 7 {
			return false
		}
		weekly++
		if first == nil {
			first = &s.Calendars[i]
		} else if *first.Hour != *c.Hour || *first.Minute != *c.Minute {
			return false
		}
	}
	return weekly == 0 || daily == 0
}

func calKey(c Calendar) string {
	f := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	wd := f(c.Weekday)
	if wd == 7 {
		wd = 0 // 7 and 0 are both Sunday
	}
	return fmt.Sprintf("mo=%d d=%d w=%d h=%d m=%d", f(c.Month), f(c.Day), wd, f(c.Hour), f(c.Minute))
}

// SameCalendars compares two calendar lists as sets (order and Weekday 7 vs 0 do not matter).
func SameCalendars(a, b []Calendar) bool {
	if len(a) != len(b) {
		return false
	}
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i], kb[i] = calKey(a[i]), calKey(b[i])
	}
	sort.Strings(ka)
	sort.Strings(kb)
	return strings.Join(ka, "|") == strings.Join(kb, "|")
}

// ParseText reads a schedule typed in one line, the way the TUI form takes it:
//
//	daily 09:00 | weekly mon,wed 09:00 | at 09:00,18:00 | every 30m | revert
//
// revert is reported separately (it restores the newest backup and has no Edit).
func ParseText(s string) (e Edit, revert bool, err error) {
	f := strings.Fields(s)
	usage := fmt.Errorf("type: daily 09:00 | weekly mon,wed 09:00 | at 09:00,18:00 | every 30m | revert")
	if len(f) == 0 {
		return Edit{}, false, usage
	}
	switch strings.ToLower(f[0]) {
	case "revert":
		if len(f) != 1 {
			return Edit{}, false, usage
		}
		return Edit{}, true, nil
	case "daily":
		if len(f) != 2 {
			return Edit{}, false, usage
		}
		e, err = Daily(f[1])
	case "at":
		if len(f) != 2 {
			return Edit{}, false, usage
		}
		e, err = At(f[1])
	case "every":
		if len(f) != 2 {
			return Edit{}, false, usage
		}
		e, err = Every(f[1])
	case "weekly":
		if len(f) != 3 {
			return Edit{}, false, usage
		}
		e, err = Weekly(f[1], f[2])
	default:
		return Edit{}, false, usage
	}
	return e, false, err
}
