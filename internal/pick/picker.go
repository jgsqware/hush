package pick

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// ErrCanceled is returned when the user leaves the picker with Esc or Ctrl-C.
var ErrCanceled = errors.New("canceled")

// maxRows is the number of items shown at once.
const maxRows = 10

// key is a decoded keypress.
type key int

const (
	keyRune key = iota
	keyUp
	keyDown
	keyEnter
	keyBackspace
	keyClear
	keyDeleteWord
	keyCancel
	keyNone
)

// model is the picker state, kept apart from the terminal so it can be tested.
type model struct {
	prompt  string
	items   []string
	query   []rune
	matches []Match
	cursor  int  // index in matches
	offset  int  // first match shown
	custom  bool // Enter with no match returns the query itself
}

func newModel(prompt string, items []string) *model {
	m := &model{prompt: prompt, items: items}
	m.refilter()
	return m
}

func (m *model) refilter() {
	m.matches = Filter(string(m.query), m.items)
	m.cursor, m.offset = 0, 0
}

// update applies a key. It returns true when the picker is done.
func (m *model) update(k key, r rune) (done bool) {
	switch k {
	case keyRune:
		m.query = append(m.query, r)
		m.refilter()
	case keyBackspace:
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
			m.refilter()
		}
	case keyClear:
		m.query = nil
		m.refilter()
	case keyDeleteWord:
		q := strings.TrimRightFunc(string(m.query), unicode.IsSpace)
		i := strings.LastIndexFunc(q, func(r rune) bool { return unicode.IsSpace(r) || r == '/' })
		m.query = []rune(q[:i+1])
		m.refilter()
	case keyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case keyDown:
		if m.cursor < len(m.matches)-1 {
			m.cursor++
		}
	case keyEnter:
		return len(m.matches) > 0 || m.custom && len(m.query) > 0
	case keyCancel:
		return true
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+maxRows {
		m.offset = m.cursor - maxRows + 1
	}
	return false
}

func (m *model) selected() string {
	if len(m.matches) == 0 {
		if m.custom {
			return strings.TrimSpace(string(m.query))
		}
		return ""
	}
	return m.matches[m.cursor].Item
}

// view renders the picker: a prompt line, then the visible matches and a counter.
// Lines are cut to width so that none wraps.
func (m *model) view(width int, color bool) []string {
	sgr := func(code, s string) string {
		if !color {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	lines := []string{sgr("1;36", m.prompt) + " " + sgr("2", "›") + " " + string(m.query)}
	end := min(m.offset+maxRows, len(m.matches))
	for i := m.offset; i < end; i++ {
		mt := m.matches[i]
		var b strings.Builder
		pos := map[int]bool{}
		for _, p := range mt.Pos {
			pos[p] = true
		}
		for j, r := range []rune(mt.Item) {
			if pos[j] {
				b.WriteString(sgr("1;33", string(r)))
			} else {
				b.WriteRune(r)
			}
		}
		if i == m.cursor {
			lines = append(lines, sgr("1;36", "▸ ")+sgr("1", b.String()))
		} else {
			lines = append(lines, "  "+b.String())
		}
	}
	help := "↑↓ move · enter select · esc cancel"
	if m.custom && len(m.matches) == 0 && len(m.query) > 0 {
		help = fmt.Sprintf("enter: use %q as typed · esc cancel", strings.TrimSpace(string(m.query)))
	}
	lines = append(lines, sgr("2", fmt.Sprintf("  %d/%d  %s", len(m.matches), len(m.items), help)))
	if width > 0 {
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, width-1, "…")
		}
	}
	return lines
}

// Options tune Choose.
type Options struct {
	Color  bool
	Custom bool // accept a typed value that matches no item
}

// Choose shows a fuzzy finder on the terminal tty and returns the chosen item.
// It draws inline below the cursor and erases itself when done.
func Choose(tty *os.File, out io.Writer, prompt string, items []string, opts Options) (string, error) {
	color := opts.Color
	fd := int(tty.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() { _ = term.Restore(fd, state) }()

	m := newModel(prompt, items)
	m.custom = opts.Custom
	// The cursor always rests on the prompt line, so each redraw clears from there.
	draw := func(final bool) {
		b := "\r\x1b[J"
		if !final {
			width, _, _ := term.GetSize(fd)
			lines := m.view(width, color)
			b += strings.Join(lines, "\r\n")
			if len(lines) > 1 {
				b += fmt.Sprintf("\x1b[%dA", len(lines)-1)
			}
			b += fmt.Sprintf("\r\x1b[%dC", ansi.StringWidth(lines[0]))
		}
		_, _ = io.WriteString(out, b)
	}
	draw(false)
	buf := make([]byte, 64)
	for {
		n, err := tty.Read(buf)
		if err != nil {
			draw(true)
			return "", err
		}
		for _, ev := range decode(buf[:n]) {
			if m.update(ev.k, ev.r) {
				draw(true)
				if ev.k == keyCancel {
					return "", ErrCanceled
				}
				return m.selected(), nil
			}
		}
		draw(false)
	}
}

type event struct {
	k key
	r rune
}

// decode turns raw terminal input into keys.
func decode(b []byte) []event {
	var evs []event
	for len(b) > 0 {
		switch {
		case b[0] == 0x1b && len(b) >= 3 && (b[1] == '[' || b[1] == 'O'):
			switch b[2] {
			case 'A':
				evs = append(evs, event{k: keyUp})
			case 'B':
				evs = append(evs, event{k: keyDown})
			}
			// Skip the rest of the sequence (parameters end with a letter or ~).
			i := 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			b = b[min(i+1, len(b)):]
			continue
		case b[0] == 0x1b:
			evs = append(evs, event{k: keyCancel})
		case b[0] == 0x03 || b[0] == 0x07: // Ctrl-C, Ctrl-G
			evs = append(evs, event{k: keyCancel})
		case b[0] == '\r' || b[0] == '\n':
			evs = append(evs, event{k: keyEnter})
		case b[0] == 0x7f || b[0] == 0x08:
			evs = append(evs, event{k: keyBackspace})
		case b[0] == 0x15: // Ctrl-U
			evs = append(evs, event{k: keyClear})
		case b[0] == 0x17: // Ctrl-W
			evs = append(evs, event{k: keyDeleteWord})
		case b[0] == 0x10 || b[0] == 0x0b: // Ctrl-P, Ctrl-K
			evs = append(evs, event{k: keyUp})
		case b[0] == 0x0e || b[0] == '\t': // Ctrl-N, Tab
			evs = append(evs, event{k: keyDown})
		case b[0] < 0x20:
			// other control keys: ignored
		default:
			r, size := utf8.DecodeRune(b)
			if r != utf8.RuneError {
				evs = append(evs, event{k: keyRune, r: r})
			}
			b = b[size:]
			continue
		}
		b = b[1:]
	}
	return evs
}
