// Package doctor checks that rtm can work on this Mac (PLAN §4, §10). Everything is read-only.
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/config"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/routine"
	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

const (
	OK   = "ok"
	Warn = "warn" // rtm still works, something is reduced
	Fail = "fail" // rtm cannot do its job
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func Run(env sys.Env) []Check {
	checks := []Check{{"user", OK, fmt.Sprintf("uid=%d home=%s", env.UID, env.Home)}}
	for _, bin := range []string{"claude", "codex"} {
		if p := which(env.Runner, bin); p != "" {
			checks = append(checks, Check{bin, OK, p})
		} else {
			// Listing and status never need them (PLAN §10), so this is only a warning.
			checks = append(checks, Check{bin, Warn, "not found by the login shell (zsh -lc 'command -v " + bin + "')"})
		}
	}
	checks = append(checks, agentsDir("user agents", filepath.Join(env.Home, "Library", "LaunchAgents"), env.UID, true))
	checks = append(checks, agentsDir("system agents", env.SystemAgentsDir, env.UID, false))
	checks = append(checks, launchctl(env))

	// Files rtm had to skip (empty plists, unreadable files, crontab trouble) show up here.
	_, warns := routine.Discover(env, nil)
	for _, w := range warns {
		checks = append(checks, Check{"scan", Warn, w})
	}

	cfg := config.Path(env.Home)
	if tracked, err := config.Load(cfg); err != nil {
		checks = append(checks, Check{"config", Fail, fmt.Sprintf("%s: %v", cfg, err)})
	} else {
		checks = append(checks, Check{"config", OK, fmt.Sprintf("%s (%d tracked)", cfg, len(tracked))})
	}
	return checks
}

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// which asks a login shell, because launchd-less terminals and GUI apps see different PATHs.
// rc files may print noise, so only the last line that looks like an absolute path counts.
func which(r sys.Runner, bin string) string {
	out, err := r.Run("zsh", "-lc", "command -v "+bin)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "/") {
			return l
		}
	}
	return ""
}

func agentsDir(name, dir string, uid int, wantWrite bool) Check {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return Check{name, Warn, dir + " does not exist (no routines there)"}
	}
	if err != nil {
		return Check{name, Fail, fmt.Sprintf("%s: %v", dir, err)}
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".plist") {
			n++
		}
	}
	detail := fmt.Sprintf("%s readable, %d plists", dir, n)
	if !wantWrite {
		return Check{name, OK, detail + " (read-only for rtm)"}
	}
	// shortcut: owner write bit only, no probe file (rtm must not write outside dev.rtm.test.* while testing).
	if fi, err := os.Stat(dir); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid && fi.Mode().Perm()&0o200 != 0 {
			return Check{name, OK, detail + ", writable"}
		}
	}
	return Check{name, Warn, detail + ", not writable (rtm schedule will not work)"}
}

func launchctl(env sys.Env) Check {
	out, err := env.Runner.Run("launchctl", "managername")
	if err != nil {
		return Check{"launchctl", Fail, err.Error()}
	}
	session := strings.TrimSpace(out)
	if session != "Aqua" { // only a GUI login session has the gui/<uid> domain
		return Check{"launchctl", Warn, fmt.Sprintf("session type %q, gui/%d may be unavailable", session, env.UID)}
	}
	return Check{"launchctl", OK, fmt.Sprintf("session Aqua, domain gui/%d", env.UID)}
}
