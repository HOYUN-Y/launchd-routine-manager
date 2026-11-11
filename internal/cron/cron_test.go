package cron

import (
	"os"
	"testing"
)

func TestParse(t *testing.T) {
	b, err := os.ReadFile("../../testdata/crontab.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, bad := Parse(string(b))
	if len(bad) != 0 {
		t.Fatalf("bad = %v", bad)
	}
	want := []Entry{
		{4, "0 9 * * *", `/opt/homebrew/bin/claude -p "daily summary" >> /tmp/summary.log 2>&1`},
		{5, "30 18 * * 1-5", "/usr/local/bin/backup-home"},
		{6, "@reboot", "/usr/local/bin/warmup"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseBadLines(t *testing.T) {
	got, bad := Parse("badline\n* * * * *\n0 9 * * * ok\n")
	if len(got) != 1 || len(bad) != 2 || bad[0] != 1 || bad[1] != 2 {
		t.Fatalf("entries=%+v bad=%v", got, bad)
	}
}
