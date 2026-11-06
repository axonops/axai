// Package colour adds optional ANSI colour to terminal output.
package colour

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	reset   = "\x1b[0m"
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	dim     = "\x1b[2m"
	bold    = "\x1b[1m"
	cyan    = "\x1b[1;36m"
	magenta = "\x1b[1;35m"
	boldYel = "\x1b[1;33m"
)

// separator is drawn above the plan summary.
const separator = "────────────────────────────────────────────────────────────"

// Enabled reports whether colour should be written to w: w must be a
// terminal, and neither NO_COLOR nor TERM=dumb may ask for plain text.
// disabled is the --no-colours flag.
func Enabled(w io.Writer, disabled bool, getenv func(string) string) bool {
	if disabled || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(on bool, code, s string) string {
	if !on {
		return s
	}
	return code + s + reset
}

// Palette paints strings for one output stream.
type Palette struct{ On bool }

// Red colours s red when colour is on.
func (p Palette) Red(s string) string { return paint(p.On, red, s) }

// Green colours s green when colour is on.
func (p Palette) Green(s string) string { return paint(p.On, green, s) }

// Yellow colours s yellow when colour is on.
func (p Palette) Yellow(s string) string { return paint(p.On, yellow, s) }

// Dim dims s when colour is on.
func (p Palette) Dim(s string) string { return paint(p.On, dim, s) }

// Status paints a doctor-style status word: OK green, FAIL red, SKIP yellow.
func (p Palette) Status(s string) string {
	switch s {
	case "OK":
		return p.Green(s)
	case "FAIL":
		return p.Red(s)
	case "SKIP":
		return p.Yellow(s)
	}
	return s
}

// rule colours a line by its prefix. stage lines also get the time since the
// writer was created; rule draws a separator above the line.
type rule struct {
	prefix, code string
	stage, rule  bool
}

var rules = []rule{
	{prefix: "warning:", code: yellow},
	{prefix: "Validation failed", code: red},
	{prefix: "Last failure", code: red},
	{prefix: "Fix attempt", code: boldYel},
	{prefix: "--- model error", code: red},
	{prefix: "--- ", code: dim},
	{prefix: "Applied and verified", code: green},
	{prefix: "all checks passed", code: green},
	{prefix: "Reading the request", code: cyan, stage: true},
	{prefix: "Gathering facts", code: cyan, stage: true},
	{prefix: "Designing the cluster", code: cyan, stage: true},
	{prefix: "Writing a role", code: cyan, stage: true},
	{prefix: "Validating the project", code: cyan, stage: true},
	{prefix: "Project:", code: magenta, rule: true},
	{prefix: "Assumptions:", code: bold},
	{prefix: "Content:", code: bold},
	{prefix: "Fixes made", code: bold},
	{prefix: "Files written by the model", code: yellow},
	{prefix: "Validation: ", code: green},
	{prefix: "Nothing was changed", code: green},
	{prefix: "Consider signing up to AxonOps", code: dim},
}

// Lines wraps w so that log lines are coloured by their prefix. The text of
// each line is unchanged, apart from the elapsed time added to stage lines
// and the separator above the plan summary. Nothing is held back waiting for
// a newline.
func Lines(w io.Writer) io.Writer { return &lines{w: w, start: true, began: time.Now()} }

// PaletteOf returns the palette matching w: on when w came from Lines.
func PaletteOf(w io.Writer) Palette {
	_, ok := w.(*lines)
	return Palette{On: ok}
}

type lines struct {
	w     io.Writer
	start bool // the next byte begins a line
	began time.Time
}

func (l *lines) Write(p []byte) (int, error) {
	var b bytes.Buffer
	rest := p
	for len(rest) > 0 {
		seg := rest
		nl := bytes.IndexByte(rest, '\n')
		if nl >= 0 {
			seg = rest[:nl]
		}
		var r *rule
		if l.start {
			for i := range rules {
				if strings.HasPrefix(string(seg), rules[i].prefix) {
					r = &rules[i]
					break
				}
			}
		}
		if r != nil {
			if r.rule {
				b.WriteString(magenta + separator + reset + "\n")
			}
			b.WriteString(r.code)
			b.Write(seg)
			b.WriteString(reset)
			if r.stage && nl >= 0 {
				b.WriteString(dim + "  +" + time.Since(l.began).Round(100*time.Millisecond).String() + reset)
			}
		} else {
			b.Write(seg)
		}
		rest = rest[len(seg):]
		l.start = false
		if nl >= 0 {
			b.WriteByte('\n')
			rest = rest[1:]
			l.start = true
		}
	}
	if _, err := l.w.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Strip removes colour escape sequences from s.
func Strip(s string) string { return ansi.ReplaceAllString(s, "") }

// StripWriter wraps w so the colour escape sequences written to it are
// dropped, keeping a log file plain while the terminal shows colour.
func StripWriter(w io.Writer) io.Writer { return stripper{w} }

type stripper struct{ w io.Writer }

func (s stripper) Write(p []byte) (int, error) {
	if _, err := s.w.Write(ansi.ReplaceAll(p, nil)); err != nil {
		return 0, err
	}
	return len(p), nil
}
