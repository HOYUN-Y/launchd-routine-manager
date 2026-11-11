package tui

// Every colour, border and lipgloss style of the TUI lives in this file and nowhere else.
// Screen code uses only the named styles in Styles and the helpers below; a test enforces it.
// Minimal on purpose: the look is refined here, by hand, without touching the screens.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette. These names and values are fixed; nothing outside this file may spell a colour.
const (
	Navy950  = lipgloss.Color("#0B1220")
	Navy900  = lipgloss.Color("#0F1828")
	Navy800  = lipgloss.Color("#162033")
	Navy700  = lipgloss.Color("#1F2B42")
	Navy600  = lipgloss.Color("#2B3A56")
	Slate400 = lipgloss.Color("#94A3B8")
	Slate300 = lipgloss.Color("#CBD5E1")
	Ivory    = lipgloss.Color("#F6F1E7")
	Gold     = lipgloss.Color("#FFB52E")
)

// StyleSet is every named style the screens may use.
type StyleSet struct {
	Header          lipgloss.Style // top bar
	ColumnHead      lipgloss.Style // table column titles
	Row             lipgloss.Style // list row
	RowSelected     lipgloss.Style // the row under the cursor
	StatusOK        lipgloss.Style // exited 0
	StatusFail      lipgloss.Style // exited non-zero
	StatusRunning   lipgloss.Style // has a live process
	StatusNotLoaded lipgloss.Style // not registered with launchd
	StatusIdle      lipgloss.Style // loaded, no result yet / crontab
	Label           lipgloss.Style // detail screen: field name
	Value           lipgloss.Style // detail screen: field value
	Notice          lipgloss.Style // status line: information
	Error           lipgloss.Style // status line: something failed
	Help            lipgloss.Style // bottom help line text
	HelpKey         lipgloss.Style // bottom help line key
	Dialog          lipgloss.Style // confirmation box
	DialogTitle     lipgloss.Style // the routine named in the box
	DialogLabel     lipgloss.Style // "// 확인" tag above it
	DialogKey       lipgloss.Style // [y] [n] inside the box
	Progress        lipgloss.Style // filled cells of the wait bar
	ProgressEmpty   lipgloss.Style // empty cells of the wait bar
	Input           lipgloss.Style // text typed into the search box / schedule form
	Body            lipgloss.Style // plain text (logs, empty screens)
	Prompt          lipgloss.Style // the "$" before the title in the header
}

// The look follows the "terminal" style of the Artificial Insight theme: a navy ground
// (Navy 800, painted by Paint), navy bands for the header, column titles and help line,
// ivory and slate text, square boxes, and gold for one thing only — what needs attention
// first: a failed run, the wait bar, the confirmation box.
var Styles = StyleSet{
	Header:          lipgloss.NewStyle().Foreground(Ivory).Background(Navy950).Bold(true),
	ColumnHead:      lipgloss.NewStyle().Foreground(Slate400).Background(Navy950),
	Row:             lipgloss.NewStyle().Foreground(Slate300),
	RowSelected:     lipgloss.NewStyle().Foreground(Ivory).Background(Navy600).Bold(true),
	StatusOK:        lipgloss.NewStyle().Foreground(Slate300),
	StatusFail:      lipgloss.NewStyle().Foreground(Gold).Bold(true),
	StatusRunning:   lipgloss.NewStyle().Foreground(Ivory).Bold(true),
	StatusNotLoaded: lipgloss.NewStyle().Foreground(Slate400),
	StatusIdle:      lipgloss.NewStyle().Foreground(Slate300),
	Label:           lipgloss.NewStyle().Foreground(Slate400),
	Value:           lipgloss.NewStyle().Foreground(Ivory),
	Notice:          lipgloss.NewStyle().Foreground(Slate300),
	Error:           lipgloss.NewStyle().Foreground(Gold).Bold(true),
	Help:            lipgloss.NewStyle().Foreground(Slate400).Background(Navy900),
	HelpKey:         lipgloss.NewStyle().Foreground(Ivory).Background(Navy900).Bold(true),
	Dialog: lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()). // square corners only
		BorderForeground(Gold).
		BorderBackground(Navy800).
		Background(Navy900).Foreground(Ivory).
		Padding(1, 3),
	DialogTitle:   lipgloss.NewStyle().Foreground(Ivory).Background(Navy900).Bold(true),
	DialogLabel:   lipgloss.NewStyle().Foreground(Slate400).Background(Navy900),
	DialogKey:     lipgloss.NewStyle().Foreground(Gold).Background(Navy900).Bold(true),
	Progress:      lipgloss.NewStyle().Foreground(Gold),
	ProgressEmpty: lipgloss.NewStyle().Foreground(Navy600),
	Input:         lipgloss.NewStyle().Foreground(Ivory),
	Body:          lipgloss.NewStyle().Foreground(Slate300),
	Prompt:        lipgloss.NewStyle().Foreground(Slate400).Background(Navy950).Bold(true),
}

// StatusKind is what a routine's state looks like in the list.
type StatusKind int

const (
	StatusKindIdle StatusKind = iota
	StatusKindOK
	StatusKindFail
	StatusKindRunning
	StatusKindNotLoaded
)

// StatusStyle is the style of a status cell. On the selected row it keeps that row's
// background, so the highlight bar is not cut by the cell's own colour.
func StatusStyle(k StatusKind, selected bool) lipgloss.Style {
	var s lipgloss.Style
	switch k {
	case StatusKindOK:
		s = Styles.StatusOK
	case StatusKindFail:
		s = Styles.StatusFail
	case StatusKindRunning:
		s = Styles.StatusRunning
	case StatusKindNotLoaded:
		s = Styles.StatusNotLoaded
	default:
		s = Styles.StatusIdle
	}
	if selected {
		s = s.Background(Navy600)
	}
	return s
}

// Cell fits text into exactly w terminal columns: cut with "…" if too long, padded with spaces
// if short. Width is measured in columns (a Hangul syllable is 2), never in bytes or runes,
// which is why the table is not built with text/tabwriter.
func Cell(text string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(text) > w {
		text = lipgloss.NewStyle().MaxWidth(w-1).Render(text) + "…"
	}
	if pad := w - lipgloss.Width(text); pad > 0 {
		text += spaces(pad)
	}
	return text
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// Clip cuts every line to w columns (no padding).
func Clip(s string, w int) string { return lipgloss.NewStyle().MaxWidth(w).Render(s) }

// Wrap breaks text into lines of at most w columns.
func Wrap(text string, w int) string { return lipgloss.NewStyle().Width(w).Render(text) }

// Center puts s in the middle of a w x h area.
func Center(w, h int, s string) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

// Fill clips s to w columns and pads it with spaces in style st, so a band (help line,
// column titles) reaches the right edge in its own colour.
func Fill(s string, w int, st lipgloss.Style) string {
	s = Clip(s, w)
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += st.Render(spaces(pad))
	}
	return s
}

// ground is the escape sequence that sets the Navy 800 background in the current colour
// profile, or "" when colours are off (NO_COLOR, not a colour terminal).
func ground() string {
	marked := lipgloss.NewStyle().Background(Navy800).Render("x")
	if i := strings.Index(marked, "x"); i > 0 {
		return marked[:i]
	}
	return ""
}

// Paint gives the whole screen the Navy 800 ground: every line is padded to w columns and
// the background is set again after each reset an inner style leaves behind. Without
// colours the screen is returned unchanged.
func Paint(screen string, w int) string {
	seq := ground()
	if seq == "" {
		return screen
	}
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		if pad := w - lipgloss.Width(l); pad > 0 {
			l += spaces(pad)
		}
		lines[i] = seq + strings.ReplaceAll(l, "\x1b[0m", "\x1b[0m"+seq) + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
