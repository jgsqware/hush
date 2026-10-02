// Package warnings parses Ansible warnings and deprecations, deduplicates
// them and attaches known fixes.
//
// Warnings reach hush two ways: as callback events (ansible-core 2.19+
// captures warnings raised during a task) and as blocks on the child's
// stderr (older versions, and warnings raised outside tasks).
package warnings

import (
	"regexp"
	"strings"
)

// Kind of a warning.
const (
	KindWarning     = "warning"
	KindDeprecation = "deprecation"
)

// Warning is one occurrence.
type Warning struct {
	Kind    string
	Msg     string
	Origin  string // "path:line:col" when known
	Context string // source snippet printed under Origin
	Help    string
	Version string // removal version, for deprecations
	Host    string // reporting host, when known
}

var (
	headerRe  = regexp.MustCompile(`^\[(WARNING|DEPRECATION WARNING)\]: ?(.*)$`)
	errorRe   = regexp.MustCompile(`^(\[ERROR\]:|ERROR!)`)
	contextRe = regexp.MustCompile(`^\s*(\d+\s|\^ column \d+)`) // line numbers are right-aligned
	// 2.16 appends this hint to every deprecation instead of a separate warning.
	inlineHint = regexp.MustCompile(`\s*Deprecation warnings can be disabled by setting\s+` + "`?" + `deprecation_warnings=False` + "`?" + `\s+in\s+ansible\.cfg\.?`)
	versionRe  = regexp.MustCompile(`removed (?:from ansible-core |in )?(?:version )?(\d+\.\d+)`)
)

// Parser turns stderr lines into warnings. Feed it lines in order with Line
// and call Flush at the end of the stream.
type Parser struct {
	cur     *Warning
	state   int
	msg     []string
	help    []string
	context []string
}

const (
	stateNone    = iota // outside any warning block
	stateMsg            // header seen, message may continue (2.16 wraps it)
	stateAfter          // blank line seen after the message
	stateContext        // inside the source snippet after "Origin:"
	stateHelp           // help text after the snippet
	stateError          // inside an error block: lines belong to the error
)

// Line consumes one stderr line. It returns the warning completed by this
// line, if any, and whether the line is not part of a warning (errors and
// other output, which callers keep to explain failures).
func (p *Parser) Line(line string) (done *Warning, other bool) {
	line = strings.TrimRight(line, "\r")
	if m := headerRe.FindStringSubmatch(line); m != nil {
		done = p.finish()
		kind := KindWarning
		if m[1] == "DEPRECATION WARNING" {
			kind = KindDeprecation
		}
		p.cur = &Warning{Kind: kind}
		p.msg = []string{m[2]}
		p.state = stateMsg
		return done, false
	}
	if errorRe.MatchString(line) {
		done = p.finish()
		p.state = stateError
		return done, true
	}

	switch p.state {
	case stateNone, stateError:
		return nil, true
	case stateMsg:
		switch {
		case strings.HasPrefix(line, "Origin: "):
			p.origin(line)
		case strings.TrimSpace(line) == "":
			p.state = stateAfter
		default:
			p.msg = append(p.msg, strings.TrimSpace(line))
		}
	case stateAfter:
		switch {
		case strings.HasPrefix(line, "Origin: "):
			p.origin(line)
		case strings.TrimSpace(line) != "":
			p.help = append(p.help, strings.TrimSpace(line))
			p.state = stateHelp
		}
	case stateContext:
		switch {
		case contextRe.MatchString(line) || strings.TrimSpace(line) == "":
			p.context = append(p.context, line)
		default:
			p.help = append(p.help, strings.TrimSpace(line))
			p.state = stateHelp
		}
	case stateHelp:
		if strings.TrimSpace(line) != "" {
			p.help = append(p.help, strings.TrimSpace(line))
		}
	}
	return nil, false
}

// Flush returns the warning still being parsed, if any.
func (p *Parser) Flush() *Warning { return p.finish() }

func (p *Parser) origin(line string) {
	p.cur.Origin = strings.TrimSpace(strings.TrimPrefix(line, "Origin: "))
	p.state = stateContext
}

func (p *Parser) finish() *Warning {
	w := p.cur
	if w != nil {
		w.Msg = strings.Join(p.msg, " ")
		w.Help = strings.Join(p.help, " ")
		w.Context = strings.Trim(strings.Join(p.context, "\n"), "\n")
		Normalize(w)
	}
	p.cur, p.msg, p.help, p.context = nil, nil, nil, nil
	p.state = stateNone
	return w
}

// Normalize cleans a warning from either source: drops the 2.16 inline hint,
// fills Version from the message, and splits an "Origin:" block (as sent by
// the callback) into Origin and Context.
func Normalize(w *Warning) {
	if loc, ctx, ok := strings.Cut(w.Origin, "\n"); ok || strings.HasPrefix(w.Origin, "Origin: ") {
		w.Origin = strings.TrimSpace(strings.TrimPrefix(loc, "Origin: "))
		if w.Context == "" {
			w.Context = strings.Trim(ctx, "\n")
		}
	}
	w.Msg = strings.TrimSpace(inlineHint.ReplaceAllString(w.Msg, ""))
	if w.Msg == "" {
		// The 2.16 hint can wrap onto its own lines; keep it as the message.
		w.Msg = "Deprecation warnings can be disabled by setting deprecation_warnings=False in ansible.cfg."
	}
	if w.Version == "" {
		if m := versionRe.FindStringSubmatch(w.Msg); m != nil {
			w.Version = m[1]
		}
	}
}
