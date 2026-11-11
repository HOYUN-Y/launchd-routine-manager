package launchd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// guardPlist is guard for calls that act on a plist FILE. launchd identifies a job by the
// Label inside the file, not by the label argument, so with the guard on the file must really
// be that test job: it has to parse, and its Label has to equal label. A file that does not
// exist yet is fine only when mustExist is false (WritePlist creating a test plist).
func guardPlist(label, path string, mustExist bool) error {
	if err := guard(label); err != nil {
		return err
	}
	if os.Getenv(GuardEnv) != "1" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !mustExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("refusing to change %q: cannot read %s to check its Label: %w", label, path, err)
	}
	p, err := Parse(data)
	if err != nil {
		return fmt.Errorf("refusing to change %q: %s does not parse as a plist with a Label: %w", label, path, err)
	}
	if p.Label != label {
		return fmt.Errorf("refusing to change %q: %s has Label %q", label, path, p.Label)
	}
	return nil
}

// CheckLabel lets callers refuse before doing any preparatory work (a backup, say).
func CheckLabel(label string) error { return guard(label) }

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
	if err := guardPlist(label, plistPath, true); err != nil {
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

// WritePlist replaces the plist at path atomically (temp file in the same directory, then
// rename), keeping the old file's permissions. This is the only function that writes a
// LaunchAgent plist.
func WritePlist(label, path string, data []byte) error {
	if err := guardPlist(label, path, false); err != nil { // the file being replaced
		return err
	}
	if os.Getenv(GuardEnv) == "1" { // and the content being written
		if p, err := Parse(data); err != nil || p.Label != label {
			return fmt.Errorf("refusing to write %q: the new content does not have Label %q (%v)", path, label, err)
		}
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	// The temp file is created exclusively (O_EXCL, random name) in the target's directory. A fixed
	// name would let a pre-planted symlink redirect the write; permissions are set on this new
	// file's own descriptor, never by path.
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".rtm-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil { // CreateTemp makes it 0600
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Lint runs `plutil -lint` on the file.
func Lint(r sys.Runner, path string) error {
	_, err := r.Run("plutil", "-lint", path)
	return err
}

// BackupDir is where schedule changes keep the previous plist (PLAN §6).
func BackupDir(home string) string { return filepath.Join(home, ".local", "state", "rtm", "backups") }

// SaveBackup stores data as <label>-<YYYYMMDD-HHMMSS>.plist (-1, -2... if that name is taken)
// and returns its path. Plists can hold secrets in EnvironmentVariables, so: dir 0700, file 0600.
// suffix is "" for a normal backup or e.g. "pre-revert", which Latest does not consider.
func SaveBackup(home, label, suffix string, now time.Time, data []byte) (string, error) {
	if err := CheckFileLabel(label); err != nil {
		return "", err
	}
	dir := BackupDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := label + "-" + now.Format("20060102-150405")
	if suffix != "" {
		base += "-" + suffix
	}
	for n := 0; n < 1000; n++ {
		name := base + ".plist"
		if n > 0 {
			name = fmt.Sprintf("%s-%d.plist", base, n)
		}
		full := filepath.Join(dir, name)
		if filepath.Dir(full) != filepath.Clean(dir) { // belt and braces after CheckFileLabel
			return "", fmt.Errorf("backup path %s is outside %s", full, dir)
		}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return full, werr
	}
	return "", errors.New("too many backups in the same second")
}

// LatestBackup finds the newest normal backup of label (not pre-revert copies, and not
// another label that merely starts with this one: a-b-2026... is not a backup of a).
func LatestBackup(home, label string) (string, error) {
	if err := CheckFileLabel(label); err != nil {
		return "", err
	}
	dir := BackupDir(home)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no backup of %s (nothing was changed with rtm schedule)", label)
	}
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(label) + `-(\d{8}-\d{6})(?:-(\d+))?\.plist$`)
	best, bestTS, bestN := "", "", -1
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2]) // "" -> 0
		if m[1] > bestTS || (m[1] == bestTS && n > bestN) {
			best, bestTS, bestN = e.Name(), m[1], n
		}
	}
	if best == "" {
		return "", fmt.Errorf("no backup of %s (nothing was changed with rtm schedule)", label)
	}
	return filepath.Join(dir, best), nil
}

// CheckFileLabel says whether label can be part of a file name inside a directory: no path
// separator, no "..", not empty, no NUL. Backups are named after the label, so a Label that
// fails this must not be edited at all.
func CheckFileLabel(label string) error {
	if label == "" || label == "." || strings.ContainsAny(label, "/\x00") || strings.Contains(label, "..") {
		return fmt.Errorf("label %q cannot be used in a file name (empty, a path separator, \"..\" or NUL)", label)
	}
	return nil
}
