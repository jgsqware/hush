package render

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jgsqware/hush/internal/agg"
)

const clearLine = "\r\x1b[2K"

var (
	spinnerUnicode = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerASCII   = []string{"|", "/", "-", `\`}
)

// TTY prints the same lines as Plain, plus one status line at the bottom
// that is redrawn in place: spinner, section, playbook, current task,
// pending hosts and elapsed time. It is safe for concurrent use, so a ticker
// can animate it while events are rendered.
type TTY struct {
	mu      sync.Mutex
	w       io.Writer
	plain   *Plain
	width   func() int       // read on every redraw, so resizes apply at once
	now     func() time.Time // injectable for tests
	start   time.Time
	frame   int
	section string
	act     agg.Activity
	shown   string // status line currently on screen
}

// NewTTY returns a live renderer. width reports the current terminal width;
// now is the clock (time.Now outside tests).
func NewTTY(w io.Writer, st Style, width func() int, now func() time.Time) *TTY {
	return &TTY{w: w, plain: NewPlain(w, st, width()), width: width, now: now}
}

// Render prints permanent lines above the status line.
func (t *TTY) Render(items ...agg.Item) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(items) == 0 {
		return nil
	}
	var b strings.Builder
	if t.shown != "" {
		b.WriteString(clearLine)
		t.shown = ""
	}
	t.plain.width = t.width()
	for _, it := range items {
		for _, l := range t.plain.Lines(it) {
			b.WriteString(l + "\n")
		}
	}
	if _, err := io.WriteString(t.w, b.String()); err != nil {
		return err
	}
	return t.redraw()
}

// SetActivity updates what the status line shows.
func (t *TTY) SetActivity(a agg.Activity) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.act = a
	return t.redraw()
}

// SetSection sets the section shown in the status line ("1/3 Docker hosts").
func (t *TTY) SetSection(s string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.section = s
	return t.redraw()
}

// Tick advances the spinner and refreshes the elapsed time.
func (t *TTY) Tick() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frame++
	return t.redraw()
}

// Close removes the status line.
func (t *TTY) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.shown == "" {
		return nil
	}
	t.shown = ""
	_, err := io.WriteString(t.w, clearLine)
	return err
}

// redraw writes the status line if it changed. Callers hold mu.
func (t *TTY) redraw() error {
	line := t.statusLine()
	if line == t.shown {
		return nil
	}
	t.shown = line
	_, err := io.WriteString(t.w, clearLine+line)
	return err
}

func (t *TTY) statusLine() string {
	if t.act.Playbook == "" && t.section == "" {
		return "" // nothing started yet
	}
	now := t.now()
	if t.start.IsZero() {
		t.start = now
	}
	st := t.plain.st
	frames := spinnerUnicode
	if st.Glyphs == ASCII {
		frames = spinnerASCII
	}

	var parts []string
	if t.section != "" {
		parts = append(parts, st.structure(t.section))
	}
	if t.act.Playbook != "" {
		parts = append(parts, st.structure(t.act.Playbook))
	}
	if t.act.Task != "" {
		parts = append(parts, t.act.Task)
	}
	l := st.tool(frames[t.frame%len(frames)]) + " " + strings.Join(parts, " "+st.meta(st.Glyphs.Playbook)+" ")

	right := st.meta(formatDuration(now.Sub(t.start)))
	if len(t.act.Pending) > 0 {
		right = st.changed(pendingHosts(t.act.Pending)) + "  " + right
	}

	// One column short of the width: writing the last column can wrap.
	w := t.width() - 1
	if w <= 0 {
		w = maxLayout
	}
	rw := ansi.StringWidth(right)
	if rw+2 >= w {
		return ansi.Truncate(l, w, st.Glyphs.More)
	}
	l = ansi.Truncate(l, w-rw-2, st.Glyphs.More)
	return l + strings.Repeat(" ", w-ansi.StringWidth(l)-rw) + right
}

// pendingHosts lists the first hosts still running, then "+N".
func pendingHosts(hosts []string) string {
	const shown = 3
	if len(hosts) <= shown {
		return strings.Join(hosts, " ")
	}
	return strings.Join(hosts[:shown], " ") + " +" + strconv.Itoa(len(hosts)-shown)
}
