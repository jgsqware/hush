package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/events"
	"github.com/jgsqware/hush/internal/render"
	"github.com/jgsqware/hush/internal/report"
	"github.com/jgsqware/hush/internal/warnings"
)

// tickEvery is the spinner and elapsed-time refresh rate of the tty status line.
const tickEvery = 120 * time.Millisecond

// output wires events through the aggregator into a renderer, and into the
// JUnit report when one is requested.
type output struct {
	agg   *agg.Aggregator
	r     render.Renderer
	live  render.Live // nil unless the renderer has a status line
	junit *report.JUnit
	path  string // JUnit output path
	stop  chan struct{}
	done  chan struct{}
	err   error // first render error; later output is dropped
	// gone is set when stdout was closed (hush ... | head): the run goes on
	// unseen, the log and reports are still written.
	gone bool
}

// newOutput picks the renderer. live is false for replays: an instant
// re-render gains nothing from a status line, so auto picks plain or ci.
func newOutput(cmd *cobra.Command, g globalFlags, opts agg.Options, live bool) (*output, error) {
	format, ciKind, err := resolveFormat(g.format, live)
	if err != nil {
		return nil, err
	}
	color, err := useColor(g.color, format)
	if err != nil {
		return nil, err
	}
	glyphs := render.ASCII
	if render.UTF8Locale() {
		glyphs = render.Unicode
	}
	st := render.Style{Color: color, Glyphs: glyphs}
	if opts.Fixes, err = warnings.Load(warnings.UserFile()); err != nil {
		return nil, fmt.Errorf("fix table: %w", err)
	}

	o := &output{agg: agg.New(opts), path: g.junit}
	w := cmd.OutOrStdout()
	switch format {
	case "tty":
		tty := render.NewTTY(w, st, termWidth, time.Now)
		o.r, o.live = tty, tty
		o.stop, o.done = make(chan struct{}), make(chan struct{})
		go o.tick()
	case "ci":
		o.r = render.NewCI(w, st, termWidth(), ciKind, time.Now)
	default:
		o.r = render.NewPlain(w, st, termWidth())
	}
	if g.junit != "" {
		o.junit = report.NewJUnit()
	}
	return o, nil
}

// resolveFormat applies --format, auto-detecting tty, then CI, then plain.
func resolveFormat(format string, live bool) (string, render.CIKind, error) {
	kind, inCI := render.DetectCI()
	switch format {
	case "tty", "plain":
		return format, kind, nil
	case "ci":
		return format, kind, nil // GitLab syntax when no CI is detected
	case "auto":
		switch {
		case live && stdoutIsTerminal():
			return "tty", kind, nil
		case inCI:
			return "ci", kind, nil
		}
		return "plain", kind, nil
	}
	return "", 0, fmt.Errorf("unknown --format %q (want auto, tty, plain or ci)", format)
}

func (o *output) tick() {
	defer close(o.done)
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-o.stop:
			return
		case <-t.C:
			_ = o.live.Tick()
		}
	}
}

func (o *output) render(items ...agg.Item) error {
	if o.err == nil && !o.gone {
		o.err = o.r.Render(items...)
		if errors.Is(o.err, syscall.EPIPE) {
			o.err, o.gone = nil, true
		}
	}
	return o.err
}

func (o *output) handle(e events.Event) {
	if o.junit != nil {
		o.junit.Handle(e)
	}
	_ = o.render(o.agg.Handle(e)...)
	if o.live != nil && o.err == nil && !o.gone {
		o.err = o.live.SetActivity(o.agg.Activity())
		if errors.Is(o.err, syscall.EPIPE) {
			o.err, o.gone = nil, true
		}
	}
}

// setSection shows a section in the live status line, if there is one.
func (o *output) setSection(s string) {
	if o.live != nil && o.err == nil && !o.gone {
		o.err = o.live.SetSection(s)
	}
}

// finish prints the closing items, writes the JUnit report and returns the
// summary.
func (o *output) finish() (agg.Summary, error) {
	if o.stop != nil {
		close(o.stop)
		<-o.done
	}
	items := o.agg.Finish()
	var sum agg.Summary
	for _, it := range items {
		if s, ok := it.(agg.Summary); ok {
			sum = s
		}
	}
	err := o.render(items...)
	if cerr := o.r.Close(); err == nil && !o.gone && !errors.Is(cerr, syscall.EPIPE) {
		err = cerr
	}
	if o.junit != nil {
		if jerr := o.junit.WriteFile(o.path); jerr != nil && err == nil {
			err = fmt.Errorf("junit: %w", jerr)
		}
	}
	return sum, err
}

func stdoutIsTerminal() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// useColor applies --color. In auto mode, CI logs get colors (GitLab and
// GitHub render ANSI) even though stdout is not a terminal.
func useColor(mode, format string) (bool, error) {
	switch mode {
	case "always":
		return true, nil
	case "never":
		return false, nil
	case "auto":
		if os.Getenv("NO_COLOR") != "" {
			return false, nil
		}
		return stdoutIsTerminal() || format == "ci", nil
	}
	return false, fmt.Errorf("unknown --color %q (want auto, always or never)", mode)
}

// termWidth returns the terminal width, $COLUMNS, or 0 (renderer default).
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil {
		return n
	}
	return 0
}
