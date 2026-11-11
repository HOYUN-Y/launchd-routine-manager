package schedule

import (
	"testing"
	"time"
	_ "time/tzdata" // the DST test must not depend on the host having zoneinfo
)

func i(n int) *int { return &n }

func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// 2026-11-12 is a Thursday.
var now = at(2026, 11, 12, 10, 30)

func TestNextCalendar(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		now  time.Time
		want time.Time
	}{
		{"daily later today", Spec{Calendars: []Calendar{{Hour: i(18), Minute: i(0)}}}, now, at(2026, 11, 12, 18, 0)},
		{"daily already passed -> tomorrow", Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}}}, now, at(2026, 11, 13, 9, 0)},
		{"exactly now is not 'after'", Spec{Calendars: []Calendar{{Hour: i(10), Minute: i(30)}}}, now, at(2026, 11, 13, 10, 30)},
		{"hourly at :15", Spec{Calendars: []Calendar{{Minute: i(15)}}}, now, at(2026, 11, 12, 11, 15)},
		{"every minute", Spec{Calendars: []Calendar{{}}}, now, at(2026, 11, 12, 10, 31)},
		{"weekday mon", Spec{Calendars: []Calendar{{Weekday: i(1), Hour: i(9), Minute: i(0)}}}, now, at(2026, 11, 16, 9, 0)},
		{"weekday 0 = sunday", Spec{Calendars: []Calendar{{Weekday: i(0), Hour: i(8), Minute: i(0)}}}, now, at(2026, 11, 15, 8, 0)},
		{"weekday 7 = sunday", Spec{Calendars: []Calendar{{Weekday: i(7), Hour: i(8), Minute: i(0)}}}, now, at(2026, 11, 15, 8, 0)},
		{"weekday today but time passed -> next week", Spec{Calendars: []Calendar{{Weekday: i(4), Hour: i(9), Minute: i(0)}}}, now, at(2026, 11, 19, 9, 0)},
		{"array picks the earliest", Spec{Calendars: []Calendar{
			{Hour: i(9), Minute: i(0)}, {Hour: i(18), Minute: i(0)}, {Hour: i(12), Minute: i(0)},
		}}, now, at(2026, 11, 12, 12, 0)},
		{"array after the last slot -> first slot tomorrow", Spec{Calendars: []Calendar{
			{Hour: i(9), Minute: i(0)}, {Hour: i(18), Minute: i(0)},
		}}, at(2026, 11, 12, 19, 0), at(2026, 11, 13, 9, 0)},
		{"day of month", Spec{Calendars: []Calendar{{Day: i(1), Hour: i(0), Minute: i(0)}}}, now, at(2026, 12, 1, 0, 0)},
		{"month+day (yearly)", Spec{Calendars: []Calendar{{Month: i(3), Day: i(5), Hour: i(7), Minute: i(0)}}}, now, at(2027, 3, 5, 7, 0)},
		{"month only", Spec{Calendars: []Calendar{{Month: i(12), Hour: i(0), Minute: i(0)}}}, now, at(2026, 12, 1, 0, 0)},
		{"month end: day 31 skips 30-day November", Spec{Calendars: []Calendar{{Day: i(31), Hour: i(6), Minute: i(0)}}}, now, at(2026, 12, 31, 6, 0)},
		{"month end: day 31 from Jan 31 evening -> Mar 31 (Feb has none)", Spec{Calendars: []Calendar{{Day: i(31), Hour: i(6), Minute: i(0)}}}, at(2027, 1, 31, 7, 0), at(2027, 3, 31, 6, 0)},
		{"day 30 across February", Spec{Calendars: []Calendar{{Day: i(30), Hour: i(6), Minute: i(0)}}}, at(2027, 1, 30, 7, 0), at(2027, 3, 30, 6, 0)},
		{"Feb 29 waits for a leap year", Spec{Calendars: []Calendar{{Month: i(2), Day: i(29), Hour: i(0), Minute: i(0)}}}, now, at(2028, 2, 29, 0, 0)},
		{"year boundary", Spec{Calendars: []Calendar{{Hour: i(0), Minute: i(0)}}}, at(2026, 12, 31, 23, 59), at(2027, 1, 1, 0, 0)},
		// Day and Weekday together: "started if either one matches" (man 5 launchd.plist)
		{"day OR weekday: day 13 matches tomorrow though Monday is later", Spec{Calendars: []Calendar{{Day: i(13), Weekday: i(1), Hour: i(9), Minute: i(0)}}}, at(2026, 11, 12, 10, 30), at(2026, 11, 13, 9, 0)},
		{"day OR weekday: Sunday comes before the next 13th", Spec{Calendars: []Calendar{{Day: i(13), Weekday: i(0), Hour: i(9), Minute: i(0)}}}, at(2026, 11, 13, 10, 0), at(2026, 11, 15, 9, 0)},
		{"day OR weekday: Saturday before the 20th", Spec{Calendars: []Calendar{{Day: i(20), Weekday: i(6), Hour: i(9), Minute: i(0)}}}, now, at(2026, 11, 14, 9, 0)},
		{"day OR weekday: both match the same day", Spec{Calendars: []Calendar{{Day: i(13), Weekday: i(5), Hour: i(9), Minute: i(0)}}}, now, at(2026, 11, 13, 9, 0)},
		{"month still restricts day OR weekday", Spec{Calendars: []Calendar{{Month: i(12), Day: i(13), Weekday: i(1), Hour: i(9), Minute: i(0)}}}, now, at(2026, 12, 7, 9, 0)}, // first Monday of Dec
	}
	for _, c := range cases {
		got, est, ok := c.spec.Next(c.now, time.Time{})
		if !ok || est || !got.Equal(c.want) {
			t.Errorf("%s: got %v (ok=%v), want %v", c.name, got, ok, c.want)
		}
	}
}

func TestNextNever(t *testing.T) {
	for name, s := range map[string]Spec{
		"RunAtLoad only": {RunAtLoad: true},
		"KeepAlive only": {KeepAlive: true, RunAtLoad: true},
		"nothing":        {},
		"month 13":       {Calendars: []Calendar{{Month: i(13)}}},
		"weekday 8":      {Calendars: []Calendar{{Weekday: i(8), Hour: i(1), Minute: i(0)}}},
		"hour 25":        {Calendars: []Calendar{{Hour: i(25), Minute: i(0)}}},
		"Feb 30":         {Calendars: []Calendar{{Month: i(2), Day: i(30)}}},
	} {
		if got, _, ok := s.Next(now, time.Time{}); ok {
			t.Errorf("%s: got %v, want none", name, got)
		}
	}
}

func TestNextInterval(t *testing.T) {
	s := Spec{Interval: 3600}
	if _, _, ok := s.Next(now, time.Time{}); ok {
		t.Error("interval without a known last run has no next time")
	}
	if got, est, _ := s.Next(now, at(2026, 11, 12, 10, 0)); !got.Equal(at(2026, 11, 12, 11, 0)) || !est {
		t.Errorf("last+interval: got %v estimated=%v, want an estimate", got, est)
	}
	if got, _, ok := s.Next(now, at(2026, 11, 12, 8, 0)); ok {
		t.Errorf("stale last run means unknown, got %v", got)
	}
}

func TestNextMixedPicksEarliest(t *testing.T) {
	s := Spec{Calendars: []Calendar{{Hour: i(18), Minute: i(0)}}, Interval: 1800}
	if got, est, _ := s.Next(now, at(2026, 11, 12, 10, 15)); !got.Equal(at(2026, 11, 12, 10, 45)) || !est {
		t.Errorf("got %v estimated=%v", got, est)
	}
}

func TestNextKeepsLocation(t *testing.T) {
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skip("no tzdata")
	}
	n := time.Date(2026, 11, 12, 23, 0, 0, 0, seoul)
	got, _, _ := Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}}}.Next(n, time.Time{})
	if want := time.Date(2026, 11, 13, 9, 0, 0, 0, seoul); !got.Equal(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		spec Spec
		want string
	}{
		{Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}}}, "daily 09:00"},
		{Spec{Calendars: []Calendar{{Weekday: i(1), Hour: i(9), Minute: i(5)}}}, "weekly mon 09:05"},
		{Spec{Calendars: []Calendar{{Weekday: i(7), Hour: i(9), Minute: i(0)}}}, "weekly sun 09:00"},
		{Spec{Calendars: []Calendar{{Minute: i(15)}}}, "hourly :15"},
		{Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}, {Hour: i(18), Minute: i(0)}}}, "daily 09:00, daily 18:00"},
		{Spec{Calendars: []Calendar{{Month: i(3), Day: i(5), Hour: i(7), Minute: i(0)}}}, "month=3 day=5 07:00"},
		{Spec{Interval: 1800}, "every 30m"},
		{Spec{Interval: 7200}, "every 2h"},
		{Spec{Interval: 45}, "every 45s"},
		{Spec{RunAtLoad: true}, "at login"},
		{Spec{KeepAlive: true, RunAtLoad: true}, "always"},
		{Spec{Interval: 3600, RunAtLoad: true}, "every 1h, at load"},
		{Spec{}, "manual"},
	}
	for _, c := range cases {
		if got := c.spec.Describe(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestParseCalendars(t *testing.T) {
	dict := map[string]any{"Hour": uint64(9), "Minute": int64(30), "Weekday": "mon"}
	got := ParseCalendars(dict)
	if len(got) != 1 || *got[0].Hour != 9 || *got[0].Minute != 30 || got[0].Day != nil {
		t.Fatalf("dict: %+v", got)
	}
	if *got[0].Weekday != -1 {
		t.Error("non-integer must become -1 (never matches), not a wildcard")
	}
	arr := ParseCalendars([]any{dict, map[string]any{"Hour": uint64(1)}, "junk"})
	if len(arr) != 2 {
		t.Fatalf("array: %+v", arr)
	}
	if ParseCalendars(nil) != nil {
		t.Error("nil -> nil")
	}
}

func TestRelative(t *testing.T) {
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{at(2026, 11, 12, 14, 0), "14:00"},
		{at(2026, 11, 13, 9, 0), "tomorrow 09:00"},
		{at(2026, 11, 20, 9, 0), "2026-11-20 09:00"},
	} {
		if got := Relative(c.t, now); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestEstimatedOnlyForInterval(t *testing.T) {
	cal := Spec{Calendars: []Calendar{{Hour: i(18), Minute: i(0)}}}
	if _, est, ok := cal.Next(now, time.Time{}); !ok || est {
		t.Errorf("calendar time is exact: est=%v ok=%v", est, ok)
	}
	iv := Spec{Interval: 3600}
	if _, est, ok := iv.Next(now, at(2026, 11, 12, 10, 0)); !ok || !est {
		t.Errorf("interval is an estimate: est=%v ok=%v", est, ok)
	}
	// a calendar time wins a tie, so the result is exact
	tie := Spec{Calendars: []Calendar{{Hour: i(11), Minute: i(0)}}, Interval: 3600}
	if got, est, _ := tie.Next(now, at(2026, 11, 12, 10, 0)); est || !got.Equal(at(2026, 11, 12, 11, 0)) {
		t.Errorf("tie: got %v est=%v", got, est)
	}
	// the interval is earlier than the calendar time: estimate
	if _, est, _ := (Spec{Calendars: []Calendar{{Hour: i(23), Minute: i(0)}}, Interval: 600}).Next(now, at(2026, 11, 12, 10, 25)); !est {
		t.Error("an earlier interval estimate must be flagged")
	}
}

// DST: wall-clock times that do not exist must be skipped, never moved to another time.
func TestNextAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	loc := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, ny) }
	// 2027-03-14: clocks jump from 02:00 EST straight to 03:00 EDT, so 02:00-02:59 never happens.
	cases := []struct {
		name string
		spec Spec
		now  time.Time
		want time.Time
	}{
		{"02:30 does not exist on the 14th: skip to the 15th",
			Spec{Calendars: []Calendar{{Hour: i(2), Minute: i(30)}}}, loc(2027, 3, 13, 12, 0), loc(2027, 3, 15, 2, 30)},
		{"just before the gap: still no earlier normalised time",
			Spec{Calendars: []Calendar{{Hour: i(2), Minute: i(30)}}}, loc(2027, 3, 14, 0, 30), loc(2027, 3, 15, 2, 30)},
		{"hourly :30 skips the missing 02:30 and lands on 03:30 EDT",
			Spec{Calendars: []Calendar{{Minute: i(30)}}}, loc(2027, 3, 14, 1, 45), loc(2027, 3, 14, 3, 30)},
		{"03:00 exists",
			Spec{Calendars: []Calendar{{Hour: i(3), Minute: i(0)}}}, loc(2027, 3, 14, 1, 0), loc(2027, 3, 14, 3, 0)},
		{"01:59 exists, the minute before the jump",
			Spec{Calendars: []Calendar{{Hour: i(1), Minute: i(59)}}}, loc(2027, 3, 14, 0, 0), loc(2027, 3, 14, 1, 59)},
		{"a weekday schedule whose time is missing waits for the next matching day",
			Spec{Calendars: []Calendar{{Weekday: i(0), Hour: i(2), Minute: i(30)}}}, loc(2027, 3, 10, 0, 0), loc(2027, 3, 21, 2, 30)}, // Sunday the 14th is skipped
	}
	for _, c := range cases {
		got, _, ok := c.spec.Next(c.now, time.Time{})
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: got %v (ok=%v), want %v", c.name, got, ok, c.want)
		}
		if ok && !got.After(c.now) {
			t.Errorf("%s: result %v is not after now %v", c.name, got, c.now)
		}
	}

	// Autumn: 2027-11-07 01:30 happens twice. Either is acceptable, but it must be 01:30 and soon.
	got, _, ok := Spec{Calendars: []Calendar{{Hour: i(1), Minute: i(30)}}}.Next(loc(2027, 11, 7, 0, 0), time.Time{})
	if !ok || got.Hour() != 1 || got.Minute() != 30 || got.Sub(loc(2027, 11, 7, 0, 0)) > 3*time.Hour {
		t.Errorf("autumn: got %v", got)
	}
}
