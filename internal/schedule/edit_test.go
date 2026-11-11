package schedule

import (
	"strings"
	"testing"
)

func TestParseTime(t *testing.T) {
	for in, want := range map[string][2]int{"09:00": {9, 0}, "00:00": {0, 0}, "23:59": {23, 59}, "9:05": {9, 5}, " 18:30 ": {18, 30}} {
		if h, m, err := ParseTime(in); err != nil || h != want[0] || m != want[1] {
			t.Errorf("%q = %d:%d %v", in, h, m, err)
		}
	}
	for _, bad := range []string{"", "9", "24:00", "09:60", "-1:00", "09:5", "ab:cd", "09:00:00", "9am"} {
		if _, _, err := ParseTime(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestBuilders(t *testing.T) {
	for _, c := range []struct {
		name string
		e    Edit
		err  error
		want string
	}{
		{"daily", must(Daily("9:05")), nil, "daily 09:05"},
		{"at two times", must(At("09:00,18:30")), nil, "daily 09:00, daily 18:30"},
		{"at dedupes", must(At("09:00,09:00,18:00")), nil, "daily 09:00, daily 18:00"},
		{"weekly", must(Weekly("mon,wed", "09:00")), nil, "weekly mon 09:00, weekly wed 09:00"},
		{"weekly case and spaces", must(Weekly("Sun, FRI", "07:15")), nil, "weekly sun 07:15, weekly fri 07:15"},
		{"weekly dedupes", must(Weekly("mon,mon", "09:00")), nil, "weekly mon 09:00"},
		{"every 30m", must(Every("30m")), nil, "every 30m"},
		{"every 2h", must(Every("2h")), nil, "every 2h"},
		{"every 90s is 90 seconds", must(Every("90s")), nil, "every 90s"},
	} {
		if got := c.e.Spec().Describe(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if (must(Every("30m"))).Interval != 1800 || len(must(Every("30m")).Calendars) != 0 {
		t.Error("Every must set the interval and no calendars")
	}
	if got := must(Weekly("sun", "08:00")).Calendars[0]; *got.Weekday != 0 {
		t.Errorf("sun is weekday 0, got %d", *got.Weekday)
	}
}

func must(e Edit, err error) Edit {
	if err != nil {
		panic(err)
	}
	return e
}

func TestBuildersRejectBadInput(t *testing.T) {
	cases := map[string]error{}
	_, cases["daily bad"] = Daily("25:00")
	_, cases["at one bad"] = At("09:00,xx")
	_, cases["at empty"] = At("")
	_, cases["weekly bad day"] = Weekly("mon,funday", "09:00")
	_, cases["weekly bad time"] = Weekly("mon", "9")
	_, cases["every garbage"] = Every("soon")
	_, cases["every too small"] = Every("30s")
	_, cases["every zero"] = Every("0")
	_, cases["every fractional second"] = Every("1m0.5s")
	_, cases["every negative"] = Every("-5m")
	for name, err := range cases {
		if err == nil {
			t.Errorf("%s should fail", name)
		}
	}
	if _, err := Every("10s"); err == nil || !strings.Contains(err.Error(), "1m") {
		t.Errorf("err = %v", err)
	}
}

func TestExpressible(t *testing.T) {
	cases := []struct {
		name string
		s    Spec
		want bool
	}{
		{"nothing", Spec{}, true},
		{"run at load only", Spec{RunAtLoad: true, KeepAlive: true}, true},
		{"interval", Spec{Interval: 1800}, true},
		{"daily", Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}}}, true},
		{"at several times", Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}, {Hour: i(18), Minute: i(0)}}}, true},
		{"weekly same time", Spec{Calendars: []Calendar{{Weekday: i(1), Hour: i(9), Minute: i(0)}, {Weekday: i(3), Hour: i(9), Minute: i(0)}}}, true},
		{"weekly different times", Spec{Calendars: []Calendar{{Weekday: i(1), Hour: i(9), Minute: i(0)}, {Weekday: i(3), Hour: i(10), Minute: i(0)}}}, false},
		{"weekly and daily mixed", Spec{Calendars: []Calendar{{Weekday: i(1), Hour: i(9), Minute: i(0)}, {Hour: i(9), Minute: i(0)}}}, false},
		{"month and day", Spec{Calendars: []Calendar{{Month: i(3), Day: i(5), Hour: i(7), Minute: i(0)}}}, false},
		{"day of month", Spec{Calendars: []Calendar{{Day: i(1), Hour: i(0), Minute: i(0)}}}, false},
		{"hourly (no Hour)", Spec{Calendars: []Calendar{{Minute: i(15)}}}, false},
		{"no Minute", Spec{Calendars: []Calendar{{Hour: i(3)}}}, false},
		{"calendar plus interval", Spec{Calendars: []Calendar{{Hour: i(9), Minute: i(0)}}, Interval: 60}, false},
		{"hour out of range", Spec{Calendars: []Calendar{{Hour: i(25), Minute: i(0)}}}, false},
		{"weekday out of range", Spec{Calendars: []Calendar{{Weekday: i(9), Hour: i(1), Minute: i(0)}}}, false},
		{"non-integer value (-1)", Spec{Calendars: []Calendar{{Hour: i(-1), Minute: i(0)}}}, false},
	}
	for _, c := range cases {
		if got := c.s.Expressible(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSameCalendars(t *testing.T) {
	a := []Calendar{{Hour: i(9), Minute: i(0)}, {Hour: i(18), Minute: i(0)}}
	b := []Calendar{{Hour: i(18), Minute: i(0)}, {Hour: i(9), Minute: i(0)}}
	if !SameCalendars(a, b) {
		t.Error("order must not matter")
	}
	if SameCalendars(a, a[:1]) || SameCalendars(a, []Calendar{{Hour: i(9), Minute: i(0)}, {Hour: i(18), Minute: i(1)}}) {
		t.Error("different sets compared equal")
	}
	if !SameCalendars([]Calendar{{Weekday: i(7), Hour: i(1), Minute: i(0)}}, []Calendar{{Weekday: i(0), Hour: i(1), Minute: i(0)}}) {
		t.Error("Weekday 7 and 0 are both Sunday")
	}
	if !SameCalendars(nil, nil) {
		t.Error("two empty lists are equal")
	}
}

func TestParseText(t *testing.T) {
	for in, want := range map[string]string{
		"daily 09:00":          "daily 09:00",
		"  Daily   9:05 ":      "daily 09:05",
		"at 09:00,18:30":       "daily 09:00, daily 18:30",
		"weekly mon,wed 09:00": "weekly mon 09:00, weekly wed 09:00",
		"every 30m":            "every 30m",
		"EVERY 2h":             "every 2h",
	} {
		e, rev, err := ParseText(in)
		if err != nil || rev || e.Spec().Describe() != want {
			t.Errorf("%q = %q rev=%v err=%v, want %q", in, e.Spec().Describe(), rev, err, want)
		}
	}
	if _, rev, err := ParseText("revert"); err != nil || !rev {
		t.Errorf("revert: %v %v", rev, err)
	}
	for _, bad := range []string{"", "   ", "daily", "daily 25:00", "daily 09:00 extra", "weekly mon", "weekly funday 09:00",
		"every", "every 10s", "at", "at 09:00 18:00", "revert now", "sometimes 09:00", "09:00"} {
		if _, rev, err := ParseText(bad); err == nil || rev {
			t.Errorf("%q must be rejected (err=%v rev=%v)", bad, err, rev)
		}
	}
}
