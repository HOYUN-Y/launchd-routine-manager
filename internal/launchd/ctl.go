package launchd

import (
	"fmt"
	"os"
	"strings"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

// These are the only functions that change launchd state. Everything else is read-only.

// TestPrefix marks labels that rtm's own tests may change state on (EXECUTION.md §4).
const TestPrefix = "dev.rtm.test."

// GuardEnv is a development safety net: when RTM_TEST_LABELS_ONLY=1, state changes are
// refused for every label except TestPrefix*, so a bug or a typo cannot touch a real routine.
// It is off by default; the product runs any label.
const GuardEnv = "RTM_TEST_LABELS_ONLY"

func guard(label string) error {
	if os.Getenv(GuardEnv) == "1" && !strings.HasPrefix(label, TestPrefix) {
		return fmt.Errorf("refusing to change %q: %s=1 allows only %s* labels", label, GuardEnv, TestPrefix)
	}
	return nil
}

func domain(uid int) string { return fmt.Sprintf("gui/%d", uid) }

// Kickstart starts the job now (launchctl kickstart [-k] gui/<uid>/<label>). With kill, a
// running instance is killed and restarted.
func Kickstart(r sys.Runner, uid int, label string, kill bool) error {
	if err := guard(label); err != nil {
		return err
	}
	args := []string{"kickstart"}
	if kill {
		args = append(args, "-k")
	}
	_, err := r.Run("launchctl", append(args, domain(uid)+"/"+label)...)
	return err
}

// Bootstrap registers a plist with launchd (launchctl bootstrap gui/<uid> <plist>).
func Bootstrap(r sys.Runner, uid int, label, plistPath string) error {
	if err := guard(label); err != nil {
		return err
	}
	_, err := r.Run("launchctl", "bootstrap", domain(uid), plistPath)
	return err
}

// Bootout unregisters the job and stops it (launchctl bootout gui/<uid>/<label>).
func Bootout(r sys.Runner, uid int, label string) error {
	if err := guard(label); err != nil {
		return err
	}
	_, err := r.Run("launchctl", "bootout", domain(uid)+"/"+label)
	return err
}
