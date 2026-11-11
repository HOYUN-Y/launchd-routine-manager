package launchd

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/schedule"
)

// A plist with every value type a real one can hold, so "other keys unchanged" means something.
const richXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>dev.rtm.test.x</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string><string>echo a &amp;&amp; echo "b" &lt; /dev/null</string></array>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin</string><key>TOKEN</key><string>s3cr3t &amp; co</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>30</integer>
<key>Nice</key><integer>-5</integer>
<key>Big</key><integer>9007199254740993</integer>
<key>Ratio</key><real>1.5</real>
<key>When</key><date>2026-01-02T03:04:05Z</date>
<key>Blob</key><data>AQIDBA==</data>
<key>Nested</key><array><dict><key>a</key><array><integer>1</integer><string>x</string><false/></array></dict></array>
<key>StartInterval</key><integer>3600</integer>
</dict></plist>
`

func decode(t *testing.T, data []byte) (map[string]any, int) {
	t.Helper()
	var m map[string]any
	f, err := plist.Unmarshal(data, &m)
	if err != nil {
		t.Fatalf("not a plist: %v\n%s", err, data)
	}
	return m, f
}

func withoutSchedule(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k != "StartCalendarInterval" && k != "StartInterval" {
			out[k] = v
		}
	}
	return out
}

func i(n int) *int { return &n }

func daily(t *testing.T, s string) schedule.Edit {
	e, err := schedule.Daily(s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestReplaceScheduleKeepsEveryOtherKeyXML(t *testing.T) {
	out, err := ReplaceSchedule([]byte(richXML), daily(t, "05:15"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("<?xml")) {
		t.Fatalf("XML in, not XML out: %.40q", out)
	}
	before, _ := decode(t, []byte(richXML))
	after, f := decode(t, out)
	if f != plist.XMLFormat {
		t.Errorf("format = %d", f)
	}
	if !reflect.DeepEqual(withoutSchedule(before), withoutSchedule(after)) {
		t.Errorf("other keys changed:\nbefore %#v\nafter  %#v", withoutSchedule(before), withoutSchedule(after))
	}
	if _, ok := after["StartInterval"]; ok {
		t.Error("the old StartInterval must be removed: a calendar schedule replaces an interval")
	}
	cal, ok := after["StartCalendarInterval"].(map[string]any)
	if !ok || cal["Hour"] != uint64(5) || cal["Minute"] != uint64(15) || len(cal) != 2 {
		t.Errorf("StartCalendarInterval = %#v, want one dict {Hour 5, Minute 15}", after["StartCalendarInterval"])
	}
	// types survive as the same XML types (not integer -> real, data -> string)
	for _, want := range []string{"<integer>-5</integer>", "<integer>9007199254740993</integer>", "<real>1.5</real>",
		"<date>2026-01-02T03:04:05Z</date>", "<data>", "&amp;&amp;", "s3cr3t &amp; co", "<false/>", "<true/>"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lost %q:\n%s", want, out)
		}
	}
}

func TestReplaceScheduleKeepsBinaryFormat(t *testing.T) {
	m, _ := decode(t, []byte(richXML))
	bin, err := plist.Marshal(m, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := schedule.Weekly("mon,wed", "06:30")
	out, err := ReplaceSchedule(bin, e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("bplist00")) {
		t.Fatalf("binary in, not binary out: %.20q", out)
	}
	after, f := decode(t, out)
	if f != plist.BinaryFormat || !reflect.DeepEqual(withoutSchedule(m), withoutSchedule(after)) {
		t.Errorf("format=%d, other keys equal=%v", f, reflect.DeepEqual(withoutSchedule(m), withoutSchedule(after)))
	}
}

func TestReplaceScheduleShapes(t *testing.T) {
	two, _ := schedule.At("09:00,18:00")
	out, err := ReplaceSchedule([]byte(richXML), two)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := decode(t, out)
	if arr, ok := m["StartCalendarInterval"].([]any); !ok || len(arr) != 2 {
		t.Errorf("several entries must be an array: %#v", m["StartCalendarInterval"])
	}

	every, _ := schedule.Every("30m")
	cal, _ := ReplaceSchedule([]byte(richXML), daily(t, "09:00"))
	out, err = ReplaceSchedule(cal, every)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = decode(t, out)
	if _, has := m["StartCalendarInterval"]; has || m["StartInterval"] != uint64(1800) {
		t.Errorf("interval must replace the calendar: %#v", m)
	}

	again, _ := ReplaceSchedule(cal, daily(t, "09:00"))
	if !bytes.Equal(again, cal) {
		t.Error("applying the same schedule twice must give identical bytes")
	}
}

func TestReplaceScheduleRefusals(t *testing.T) {
	for name, data := range map[string]string{
		"garbage":        "not a plist",
		"array root":     `<?xml version="1.0"?><plist version="1.0"><array><string>x</string></array></plist>`,
		"openstep text":  `{ Label = "x"; StartInterval = 60; }`,
		"empty document": ``,
	} {
		if _, err := ReplaceSchedule([]byte(data), daily(t, "09:00")); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, err := ReplaceSchedule([]byte(richXML), schedule.Edit{}); err == nil {
		t.Error("an empty schedule must be refused")
	}
	if _, err := ReplaceSchedule([]byte(richXML), schedule.Edit{Interval: 60, Calendars: []schedule.Calendar{{Hour: i(1), Minute: i(0)}}}); err == nil {
		t.Error("calendar and interval together must be refused")
	}
}
