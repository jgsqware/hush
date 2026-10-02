// Package render prints aggregator items: plain (append-only), tty (plain
// plus a live status line) and ci (plain plus collapsible sections).
package render

import (
	"os"
	"strings"

	"github.com/jgsqware/hush/internal/agg"
)

// Style holds the color and glyph choices of a renderer.
type Style struct {
	Color  bool
	Glyphs Glyphs
}

// Semantic colors only (ANSI 256 palette, readable on dark and light themes).
const (
	green  = "38;5;71"
	amber  = "38;5;214"
	red    = "38;5;203"
	gray   = "38;5;245"
	blue   = "38;5;75"
	purple = "38;5;141"
	bold   = "1"
)

func (s Style) paint(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) ok(t string) string      { return s.paint(green, t) }
func (s Style) changed(t string) string { return s.paint(amber, t) }
func (s Style) failed(t string) string  { return s.paint(red, t) }
func (s Style) meta(t string) string    { return s.paint(gray, t) }
func (s Style) structure(t string) string {
	return s.paint(blue, t)
}
func (s Style) tool(t string) string { return s.paint(purple, t) }
func (s Style) bold(t string) string { return s.paint(bold, t) }

// Glyphs used by the renderers.
type Glyphs struct {
	Banner, Playbook, OK, Changed, Skip, Failed, Ignored, Rescued, Rerun string
	Rule, BoxTop, BoxSide, BoxEnd, BoxOne, Success, Failure, Warn, More  string
	Interrupted                                                          string
}

// Unicode glyphs.
var Unicode = Glyphs{
	Banner: "◆", Playbook: "▸", OK: "✓", Changed: "●", Skip: "⊘", Failed: "✗",
	Ignored: "~", Rescued: "↺", Rerun: "↻", Rule: "━",
	BoxTop: "╭─", BoxSide: "│", BoxEnd: "╰─", BoxOne: "──",
	Success: "✔", Failure: "✘", Warn: "⚠", More: "…", Interrupted: "■",
}

// ASCII glyphs, for locales that are not UTF-8.
var ASCII = Glyphs{
	Banner: "*", Playbook: ">", OK: "+", Changed: "*", Skip: "-", Failed: "x",
	Ignored: "~", Rescued: "@", Rerun: ">", Rule: "=",
	BoxTop: "+-", BoxSide: "|", BoxEnd: "+-", BoxOne: "--",
	Success: "OK", Failure: "FAIL", Warn: "!", More: "...", Interrupted: "#",
}

// UTF8Locale reports whether the locale environment asks for UTF-8.
func UTF8Locale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}

// status returns the glyph and painter for a task status.
func (s Style) status(st agg.Status) (string, func(string) string) {
	g := s.Glyphs
	switch st {
	case agg.StatusChanged:
		return g.Changed, s.changed
	case agg.StatusSkipped:
		return g.Skip, s.meta
	case agg.StatusIgnored:
		return g.Ignored, s.meta
	case agg.StatusRescued:
		return g.Rescued, s.meta
	case agg.StatusFailed, agg.StatusUnreachable:
		return g.Failed, s.failed
	default:
		return g.OK, s.ok
	}
}
