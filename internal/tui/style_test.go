package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The rule: colours and lipgloss styles are defined in style.go only. Screen code uses the
// named styles. These checks read the sources, so the rule cannot rot unnoticed.
func TestStylesLiveOnlyInStyleGo(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	forbidden := []string{
		"lipgloss.Color", "lipgloss.NewStyle", "lipgloss.AdaptiveColor", "lipgloss.CompleteColor",
		"RoundedBorder", "ThickBorder", "DoubleBorder", "HiddenBorder", "lipgloss.Border", "NormalBorder",
		".Foreground(", ".Background(", ".BorderForeground(", `"#`,
	}
	for _, f := range files {
		if f == "style.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, bad := range forbidden {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s uses %q: move it into style.go", f, bad)
			}
		}
	}
}

func TestPaletteIsExactlyAsSpecified(t *testing.T) {
	b, _ := os.ReadFile("style.go")
	src := string(b)
	for name, hex := range map[string]string{
		"Navy950": "#0B1220", "Navy900": "#0F1828", "Navy800": "#162033", "Navy700": "#1F2B42", "Navy600": "#2B3A56",
		"Slate400": "#94A3B8", "Slate300": "#CBD5E1", "Ivory": "#F6F1E7", "Gold": "#FFB52E",
	} {
		re := regexp.MustCompile(name + `\s+= lipgloss\.Color\("` + regexp.QuoteMeta(hex) + `"\)`)
		if !re.MatchString(src) {
			t.Errorf("%s must be %s", name, hex)
		}
	}
	if n := strings.Count(src, `lipgloss.Color("#`); n != 9 {
		t.Errorf("style.go spells %d colours, want exactly the 9 named ones", n)
	}
	for _, bad := range []string{"RoundedBorder", "ThickBorder", "DoubleBorder"} {
		if strings.Contains(src, bad) {
			t.Errorf("style.go uses %s: only square borders (NormalBorder)", bad)
		}
	}
	if !strings.Contains(src, "NormalBorder()") {
		t.Error("the dialog must use the square NormalBorder")
	}
}

func TestNamedStylesExist(t *testing.T) {
	v := reflect.ValueOf(Styles)
	for _, name := range []string{"Header", "Row", "RowSelected", "StatusOK", "StatusFail", "StatusRunning", "StatusNotLoaded", "Help", "Dialog", "Progress"} {
		if !v.FieldByName(name).IsValid() {
			t.Errorf("Styles.%s is missing", name)
		}
	}
}

func TestPaintFillsEveryLineWithTheGround(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(termenv.TrueColor)
	seq := ground()
	if seq == "" {
		t.Fatal("no background sequence with colours on")
	}
	screen := Styles.Header.Render("rtm") + " 루틴\n" + Styles.StatusFail.Render("실패 1") + " tail\n"
	out := Paint(screen, 20)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w != 20 {
			t.Errorf("line %d is %d columns, want 20: %q", i, w, l)
		}
		if !strings.HasPrefix(l, seq) {
			t.Errorf("line %d does not start on the ground: %q", i, l)
		}
		// after every reset an inner style leaves, the ground comes back
		if strings.Count(l, "\x1b[0m") != strings.Count(l, "\x1b[0m"+seq)+1 {
			t.Errorf("line %d has a reset without the ground after it: %q", i, l)
		}
	}

	lipgloss.SetColorProfile(termenv.Ascii)
	if got := Paint("plain\ntext", 20); got != "plain\ntext" {
		t.Errorf("without colours the screen must be unchanged, got %q", got)
	}
}
