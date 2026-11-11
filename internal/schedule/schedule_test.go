package schedule

import (
	"testing"
	"time"
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
		{"day AND weekday must both match", Spec{Calendars: []Calendar{{Day: i(13), Weekday: i(1), Hour: i(9), Minute: i(0)}}}, now, at(2027, 9, 13, 9, 0)}, // first Monday-the-13th after Nov 2026
		{"day AND weekday: Friday the 13th is tomorrow", Spec{Calendars: []Calendar{{Day: i(13), Weekday: i(5), Hour: i(9), Minute: i(0)}}}, now, at(2026, 11, 13, 9, 0)},
	}
	for _, c := range cases {
		got, ok := c.spec.Next(c.now, time.Time{})
		if !ok || !got.Equal(c.want) {
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
		if got, ok := s.Next(now, time.Time{}); ok {
			t.Errorf("%s: got %v, want none", name, got)
		}
	}
}

func TestNextInterval(t *testing.T) {
	s := Spec{Interval: 3600}
	if _, ok := s.Next(now, time.Time{}); ok {
		t.Error("interval without a known last run has no next time")
	}
	if got, _ := s.Next(now, at(2026, 11, 12, 10, 0)); !got.Equal(at(2026, 11, 12, 11, 0)) {
		t.Errorf("last+interval: got %v", got)
	}
	if got, ok := s.Next(now, at(2026, 11, 12, 8, 0)); ok {
		t.Errorf("stale last run means unknown, got %v", got)
	}
}

func TestNextMixedPicksEarliest(t *testing.T) {
	s := Spec{Calendars: []Calendar{{Hour: i(18), Minute: i(0)}}, Interval: 1800}
	if got, _ := s.Next(now, at(2026, 11, 12, 10, 15)); !got.Equal(at(2026, 11, 12, 10, 45)) {
		t.Errorf("got %v", got)
	}
}

func TestNextKeepsLocation(t *testing.T) {
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skip("no tzdata")
	}
	n := time.Date(2026, 11, 12, 23, 0, 0, 0, seoul)
	got, _ := Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}}}.Next(n, time.Time{})
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
