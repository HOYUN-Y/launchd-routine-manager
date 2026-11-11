package launchd

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"

	"howett.net/plist"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
)

const (
	keyCalendar = "StartCalendarInterval"
	keyInterval = "StartInterval"
)

// ReplaceSchedule returns the plist with StartCalendarInterval and StartInterval replaced by e,
// in the same format (XML or binary) as the input. Every other key is carried over unchanged.
//
// The result is decoded again and checked before it is returned: all other keys must be
// identical and the schedule must read back as asked. If the plist library cannot round-trip a
// file faithfully, this fails instead of producing a damaged file (PLAN §11).
// shortcut: comments, key order and whitespace of an XML plist are not kept (decoded values are);
// to keep them byte for byte, edit the XML text instead.
func ReplaceSchedule(data []byte, e schedule.Edit) ([]byte, error) {
	var orig map[string]any
	format, err := plist.Unmarshal(data, &orig)
	if err != nil {
		return nil, fmt.Errorf("cannot parse plist: %w", err)
	}
	if format != plist.XMLFormat && format != plist.BinaryFormat {
		return nil, errors.New("only XML and binary plists can be edited")
	}

	next := make(map[string]any, len(orig)+1)
	for k, v := range orig {
		if k != keyCalendar && k != keyInterval {
			next[k] = v
		}
	}
	switch {
	case e.Interval > 0 && len(e.Calendars) > 0:
		return nil, errors.New("a schedule is either calendar entries or an interval, not both")
	case e.Interval > 0:
		next[keyInterval] = e.Interval
	case len(e.Calendars) == 1:
		next[keyCalendar] = calendarDict(e.Calendars[0])
	case len(e.Calendars) > 1:
		arr := make([]any, len(e.Calendars))
		for i, c := range e.Calendars {
			arr[i] = calendarDict(c)
		}
		next[keyCalendar] = arr
	default:
		return nil, errors.New("empty schedule")
	}

	var buf bytes.Buffer
	enc := plist.NewEncoderForFormat(&buf, format)
	if format == plist.XMLFormat {
		enc.Indent("\t")
	}
	if err := enc.Encode(next); err != nil {
		return nil, err
	}

	// read it back and compare
	var back map[string]any
	if f2, err := plist.Unmarshal(buf.Bytes(), &back); err != nil || f2 != format {
		return nil, fmt.Errorf("edited plist does not read back (format %d -> %d): %v", format, f2, err)
	}
	for k, v := range orig {
		if k == keyCalendar || k == keyInterval {
			continue
		}
		if !reflect.DeepEqual(v, back[k]) {
			return nil, fmt.Errorf("refusing to write: key %q would change in the round trip", k)
		}
	}
	for k := range back {
		if _, ok := orig[k]; !ok && k != keyCalendar && k != keyInterval {
			return nil, fmt.Errorf("refusing to write: unexpected new key %q", k)
		}
	}
	got := scheduleOf(back)
	if got.Interval != e.Interval || !schedule.SameCalendars(got.Calendars, e.Calendars) {
		return nil, fmt.Errorf("refusing to write: schedule reads back as %q, wanted %q", got.Describe(), e.Spec().Describe())
	}
	return buf.Bytes(), nil
}

func calendarDict(c schedule.Calendar) map[string]any {
	m := map[string]any{}
	for k, p := range map[string]*int{"Minute": c.Minute, "Hour": c.Hour, "Day": c.Day, "Month": c.Month, "Weekday": c.Weekday} {
		if p != nil {
			m[k] = *p
		}
	}
	return m
}

func scheduleOf(m map[string]any) schedule.Spec {
	s := schedule.Spec{Calendars: schedule.ParseCalendars(m[keyCalendar])}
	switch v := m[keyInterval].(type) {
	case uint64:
		s.Interval = int(v)
	case int64:
		s.Interval = int(v)
	}
	return s
}
