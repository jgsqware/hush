package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jgsqware/hush/internal/agg"
)

// CIKind selects the collapsible-section syntax.
type CIKind int

const (
	GitLab CIKind = iota
	GitHub
)

// DetectCI returns the CI system from the environment, if any.
func DetectCI() (CIKind, bool) {
	switch {
	case os.Getenv("GITLAB_CI") != "":
		return GitLab, true
	case os.Getenv("GITHUB_ACTIONS") != "":
		return GitHub, true
	}
	return 0, false
}

// CI is Plain plus one collapsible section per playbook. Failures and the
// playbook recap are printed outside sections so a collapsed section never
// hides them.
type CI struct {
	plain *Plain
	w     io.Writer
	kind  CIKind
	now   func() time.Time // section timestamps (GitLab shows durations)
	n     int
	open  string // name of the open section, if any
}

// NewCI returns a CI renderer.
func NewCI(w io.Writer, st Style, width int, kind CIKind, now func() time.Time) *CI {
	return &CI{plain: NewPlain(w, st, width), w: w, kind: kind, now: now}
}

// Render prints items, opening and closing sections around playbooks.
func (c *CI) Render(items ...agg.Item) error {
	var b strings.Builder
	for _, it := range items {
		switch v := it.(type) {
		case agg.PlaybookHeader:
			c.closeSection(&b)
			c.openSection(&b, v)
			continue
		case agg.Failure, agg.PlaybookRecap, agg.Summary:
			c.closeSection(&b)
		}
		for _, l := range c.plain.Lines(it) {
			b.WriteString(l + "\n")
		}
	}
	_, err := io.WriteString(c.w, b.String())
	return err
}

// Close ends a section left open.
func (c *CI) Close() error {
	var b strings.Builder
	c.closeSection(&b)
	_, err := io.WriteString(c.w, b.String())
	return err
}

func (c *CI) openSection(b *strings.Builder, h agg.PlaybookHeader) {
	c.n++
	header := strings.Join(c.plain.Lines(h), " ")
	switch c.kind {
	case GitLab:
		c.open = fmt.Sprintf("hush_playbook_%d", c.n)
		fmt.Fprintf(b, "\x1b[0Ksection_start:%d:%s[collapsed=false]\r\x1b[0K%s\n", c.now().Unix(), c.open, header)
	case GitHub:
		c.open = "group"
		// Group titles are plain text.
		fmt.Fprintf(b, "::group::%s\n", strings.TrimSpace(ansi.Strip(header)))
	}
}

func (c *CI) closeSection(b *strings.Builder) {
	if c.open == "" {
		return
	}
	switch c.kind {
	case GitLab:
		fmt.Fprintf(b, "\x1b[0Ksection_end:%d:%s\r\x1b[0K\n", c.now().Unix(), c.open)
	case GitHub:
		b.WriteString("::endgroup::\n")
	}
	c.open = ""
}
