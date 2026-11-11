// Package cron parses `crontab -l` output.
package cron

import (
	"regexp"
	"strings"
)

type Entry struct {
	Line     int // 1-based line in the crontab
	Schedule string
	Command  string
}

var (
	envLine  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*=`)
	fiveTime = regexp.MustCompile(`^(\S+\s+){5}`) // minute hour dom month dow
	special  = regexp.MustCompile(`^@\S+\s+`)     // @reboot, @daily, ...
)

// Parse skips blanks, comments and VAR=value lines. Lines it cannot read are returned in bad.
func Parse(text string) (entries []Entry, bad []int) {
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || envLine.MatchString(line) {
			continue
		}
		re := fiveTime
		if strings.HasPrefix(line, "@") {
			re = special
		}
		sched := re.FindString(line)
		cmd := strings.TrimSpace(line[len(sched):])
		if sched == "" || cmd == "" {
			bad = append(bad, i+1)
			continue
		}
		entries = append(entries, Entry{Line: i + 1, Schedule: strings.Join(strings.Fields(sched), " "), Command: cmd})
	}
	return entries, bad
}
