package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/warnings"
)

const (
	// maxLayout caps the layout width so columns stay readable on wide terminals.
	maxLayout = 100
	// maxBlockLines is how many stdout/stderr lines are shown before "+N lines".
	maxBlockLines = 8
	// maxInfoLines is the same for debug output, which is meant to be read.
	maxInfoLines = 12
	// nameColumn is the preferred width of task names.
	nameColumn = 40
)

// Plain is the append-only renderer: no cursor movement, safe for pipes and logs.
type Plain struct {
	w     io.Writer
	st    Style
	width int
}

// NewPlain returns a plain renderer writing to w, laid out for the given
// terminal width.
func NewPlain(w io.Writer, st Style, width int) *Plain {
	return &Plain{w: w, st: st, width: width}
}

// Render prints items.
func (p *Plain) Render(items ...agg.Item) error {
	for _, it := range items {
		for _, l := range p.Lines(it) {
			if _, err := io.WriteString(p.w, l+"\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close is a no-op; plain output has nothing to clean up.
func (p *Plain) Close() error { return nil }

func (p *Plain) layout() int {
	if p.width <= 0 || p.width > maxLayout {
		return maxLayout
	}
	return p.width
}

// Lines formats one item as terminal lines (without newlines).
func (p *Plain) Lines(it agg.Item) []string {
	var lines []string
	switch v := it.(type) {
	case agg.Banner:
		lines = p.banner(v)
	case agg.PlaybookHeader:
		lines = p.header(v)
	case agg.TaskLine:
		lines = p.taskLine(v)
	case agg.InfoBox:
		lines = p.infoBox(v)
	case agg.Failure:
		lines = p.failure(v)
	case agg.Notice:
		lines = []string{"   " + p.st.meta(v.Text)}
	case agg.SectionHeader:
		lines = []string{"", p.rule(fmt.Sprintf("%d/%d  %s", v.Index, v.Count, v.Name))}
	case agg.HookLine:
		lines = p.hook(v)
	case agg.GuardLine:
		lines = p.guard(v)
	case agg.PlaybookRecap:
		lines = p.recap(v)
	case agg.Summary:
		lines = p.summary(v)
	}
	if _, ok := it.(agg.Failure); ok {
		return lines // wrapped already; the rerun command must stay whole to be copied
	}
	w := p.layout()
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, p.st.Glyphs.More)
	}
	return lines
}

func (p *Plain) banner(b agg.Banner) []string {
	g := p.st.Glyphs
	line := p.st.tool(g.Banner+" hush") + "  " + b.Title
	if b.Detail != "" {
		line += "  " + p.st.meta(b.Detail)
	}
	return []string{line, ""}
}

func (p *Plain) header(h agg.PlaybookHeader) []string {
	line := " " + p.st.structure(p.st.Glyphs.Playbook+" "+h.Name) + "   " + p.st.meta(strings.Join(h.Hosts, " "))
	if h.Check {
		line += "  " + p.st.changed("check mode")
	}
	return []string{line}
}

func (p *Plain) taskLine(t agg.TaskLine) []string {
	glyph, paint := p.st.status(t.Status)

	// tail must stay visible; extra (labels, notes) is cut at the line end.
	var tail, extra []string
	if hosts := p.hostGroups(t); hosts != "" {
		tail = append(tail, hosts)
	}
	if t.Loop != nil {
		ratio, labels := p.loop(t.Loop)
		tail = append(tail, ratio)
		extra = append(extra, labels)
	}
	switch t.Status {
	case agg.StatusRescued:
		extra = append(extra, p.st.meta("rescued: "+firstLine(t.Note)))
	case agg.StatusIgnored:
		extra = append(extra, p.st.meta("ignored: "+firstLine(t.Note)))
	}
	line := p.columns("   "+paint(glyph)+" ", t.Name, strings.Join(tail, "  "))
	if e := strings.TrimSpace(strings.Join(extra, "  ")); e != "" {
		line += "  " + e
	}
	return []string{line}
}

// columns lays out "prefix name   tail", truncating the name so the tail fits.
func (p *Plain) columns(prefix, name, tail string) string {
	if tail == "" {
		return prefix + name
	}
	avail := p.layout() - ansi.StringWidth(prefix) - 2 - ansi.StringWidth(tail)
	col := min(nameColumn, max(avail, 16))
	if ansi.StringWidth(name) > col {
		name = ansi.Truncate(name, col, p.st.Glyphs.More)
	}
	return prefix + pad(name, col) + "  " + tail
}

func (p *Plain) hostGroups(t agg.TaskLine) string {
	if len(t.Groups) == 1 {
		if t.OneHost {
			return ""
		}
		return strings.Join(t.Groups[0].Hosts, " ")
	}
	var parts []string
	for i, g := range t.Groups {
		hosts := strings.Join(g.Hosts, " ")
		if i == 0 {
			parts = append(parts, hosts)
			continue
		}
		glyph, paint := p.st.status(g.Status)
		parts = append(parts, paint(glyph+" "+hosts))
	}
	return strings.Join(parts, "  ")
}

// loop returns the item ratio and the changed labels.
func (p *Plain) loop(l *agg.Loop) (ratio, labels string) {
	ratio = fmt.Sprintf("%d/%d items", l.Changed, l.Total)
	if len(l.PerHost) > 0 {
		ratio = strings.Join(l.PerHost, " · ") + " items"
	}
	if len(l.Labels) > 0 {
		labels = strings.Join(l.Labels, ", ")
		if l.MoreLabels > 0 {
			labels += fmt.Sprintf(" +%d", l.MoreLabels)
		}
	}
	return ratio, p.st.meta(labels)
}

func (p *Plain) infoBox(b agg.InfoBox) []string {
	g := p.st.Glyphs
	title := p.st.structure("info")
	if !b.AllHosts {
		title += " " + p.st.meta(strings.Join(b.Hosts, " "))
	}
	limit := max(p.layout()-5, 20)
	var lines []string
	for _, l := range b.Lines {
		lines = append(lines, strings.Split(ansi.Wrap(l, limit, " /"), "\n")...)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	var hidden int
	if len(lines) > maxInfoLines {
		hidden = len(lines) - maxInfoLines
		lines = lines[:maxInfoLines]
	}
	if len(lines) == 1 && hidden == 0 {
		return []string{"   " + p.st.structure(g.BoxOne) + " " + title + "  " + lines[0]}
	}
	out := []string{"   " + p.st.structure(g.BoxTop) + " " + title + "  " + lines[0]}
	for _, l := range lines[1 : len(lines)-1] {
		out = append(out, "   "+p.st.structure(g.BoxSide)+" "+l)
	}
	last := lines[len(lines)-1]
	if hidden > 0 {
		out = append(out, "   "+p.st.structure(g.BoxSide)+" "+last)
		last = p.st.meta(fmt.Sprintf("%s +%d lines", g.More, hidden))
	}
	return append(out, "   "+p.st.structure(g.BoxEnd)+" "+last)
}

func (p *Plain) failure(f agg.Failure) []string {
	g := p.st.Glyphs
	what := f.Host
	if f.Unreachable {
		what += "  unreachable"
	}
	out := []string{p.columns("   "+p.st.failed(g.Failed)+" ", p.st.failed(f.Task), p.st.failed(what))}
	const in = "     "
	if f.Location != "" {
		out = append(out, in+p.st.meta("at ")+f.Location)
	}
	out = append(out, p.yamlField(in, "msg", f.Msg, 0)...)
	if f.RC != nil && *f.RC != 0 {
		out = append(out, in+p.st.meta("rc: ")+strconv.Itoa(*f.RC))
	}
	out = append(out, p.yamlField(in, "stdout", f.Stdout, f.StdoutTotal)...)
	out = append(out, p.yamlField(in, "stderr", f.Stderr, f.StderrTotal)...)
	if len(f.FailedItems) > 0 {
		out = append(out, in+p.st.meta("items:"))
		for _, it := range f.FailedItems {
			out = append(out, in+"  "+p.st.meta("- ")+it)
		}
	}
	if f.Rerun != "" {
		out = append(out, in+p.st.structure(g.Rerun+" rerun")+"  "+f.Rerun)
	}
	return out
}

// yamlField prints key: value, switching to a block scalar for multi-line
// values and truncating long ones with a "+N lines" hint.
func (p *Plain) yamlField(indent, key, value string, total int) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	// Wrap rather than truncate: these lines are the reason the run failed.
	limit := max(p.layout()-ansi.StringWidth(indent)-2, 20)
	lines := strings.Split(ansi.Wrap(value, limit, " /"), "\n")
	if len(lines) == 1 && total <= 1 && ansi.StringWidth(key)+2+ansi.StringWidth(value) <= limit {
		return []string{indent + p.st.meta(key+": ") + value}
	}
	out := []string{indent + p.st.meta(key+": |")}
	shown := lines
	if len(shown) > maxBlockLines {
		shown = shown[:maxBlockLines]
	}
	for _, l := range shown {
		out = append(out, indent+"  "+l)
	}
	if hidden := max(total, len(lines)) - len(shown); hidden > 0 {
		out = append(out, indent+"  "+p.st.meta(fmt.Sprintf("%s +%d lines", p.st.Glyphs.More, hidden)))
	}
	return out
}

func (p *Plain) counts(c agg.TaskCounts) string {
	g := p.st.Glyphs
	var parts []string
	add := func(n int, s string) {
		if n > 0 {
			parts = append(parts, s)
		}
	}
	add(c.OK, p.st.ok(fmt.Sprintf("%s %d ok", g.OK, c.OK)))
	add(c.Changed, p.st.changed(fmt.Sprintf("%s %d changed", g.Changed, c.Changed)))
	add(c.Skipped, p.st.meta(fmt.Sprintf("%s %d skip", g.Skip, c.Skipped)))
	add(c.Rescued, p.st.meta(fmt.Sprintf("%s %d rescued", g.Rescued, c.Rescued)))
	add(c.Ignored, p.st.meta(fmt.Sprintf("%s %d ignored", g.Ignored, c.Ignored)))
	add(c.Failed, p.st.failed(fmt.Sprintf("%s %d failed", g.Failed, c.Failed)))
	if len(parts) == 0 {
		return p.st.meta("no tasks")
	}
	return strings.Join(parts, "  ")
}

func (p *Plain) recap(r agg.PlaybookRecap) []string {
	dur := p.st.meta(formatDuration(r.Duration))
	if r.HeaderShown {
		return []string{p.rightAlign("   "+p.counts(r.Counts), dur)}
	}
	left := " " + p.st.structure(p.st.Glyphs.Playbook+" "+r.Name) + "   " + p.st.meta(strings.Join(r.Hosts, " ")) +
		"   " + p.counts(r.Counts)
	if r.Counts.Changed == 0 && r.Counts.Failed == 0 {
		left += "   " + p.st.meta("unchanged")
	}
	if r.Check {
		left += "  " + p.st.changed("check mode")
	}
	return []string{p.rightAlign(left, dur)}
}

func (p *Plain) rightAlign(left, right string) string {
	gap := p.layout() - ansi.StringWidth(left) - ansi.StringWidth(right)
	return left + strings.Repeat(" ", max(gap, 2)) + right
}

func (p *Plain) rule(title string) string {
	g := p.st.Glyphs.Rule
	head := g + g + " " + title + " "
	return p.st.structure(head + strings.Repeat(g, max(p.layout()-ansi.StringWidth(head), 3)))
}

func (p *Plain) summary(s agg.Summary) []string {
	g := p.st.Glyphs
	var verdict string
	switch s.Verdict {
	case agg.VerdictSuccess:
		verdict = p.st.ok(p.st.bold(g.Success + " SUCCESS"))
	case agg.VerdictInterrupted:
		verdict = p.st.changed(p.st.bold(g.Interrupted + " INTERRUPTED"))
	default:
		verdict = p.st.failed(p.st.bold(fmt.Sprintf("%s FAILED", g.Failure))) + p.st.meta(fmt.Sprintf(" (exit %d)", s.ExitCode))
	}
	facts := []string{
		plural(s.Playbooks, "playbook"),
		plural(len(s.Hosts), "host"),
		fmt.Sprintf("%d failed", s.Failed),
		fmt.Sprintf("%d changed", s.Changed),
		formatDuration(s.Duration),
	}
	if s.NotRun > 0 {
		facts = append(facts[:1], append([]string{p.st.changed(fmt.Sprintf("%d not run", s.NotRun))}, facts[1:]...)...)
	}
	out := []string{"", p.rule("Summary"), " " + verdict + "  " + strings.Join(facts, " · ")}
	if len(s.Hosts) > 0 {
		out = append(out, "")
		out = append(out, p.hostTable(s.Hosts)...)
	}
	out = append(out, p.warnings(s.Warnings)...)
	if s.LogPath != "" {
		out = append(out, "", " "+p.st.meta("logs")+"  "+s.LogPath)
	}
	return out
}

func (p *Plain) hook(h agg.HookLine) []string {
	g := p.st.Glyphs
	glyph, label := p.st.ok(g.OK), ""
	if h.ExitCode != 0 {
		glyph, label = p.st.failed(g.Failed), p.st.failed(fmt.Sprintf("exit %d", h.ExitCode))
	}
	left := " " + glyph + " " + p.st.structure("before") + " " + h.Name
	if len(h.Summary) > 0 {
		left += "  " + strings.Join(h.Summary, " · ")
	}
	if label != "" {
		left += "  " + label
	}
	out := []string{p.rightAlign(left, p.st.meta(formatDuration(h.Duration)))}
	for _, w := range h.Warnings {
		out = append(out, "   "+p.st.changed(g.Warn+" "+w))
	}
	for _, l := range h.Tail {
		out = append(out, "     "+p.st.meta(l))
	}
	return out
}

func (p *Plain) guard(gl agg.GuardLine) []string {
	g := p.st.Glyphs
	cidrs := strings.Join(gl.CIDRs, ", ")
	switch {
	case gl.Error != "":
		return []string{" " + p.st.failed(g.Failed+" guard") + "  " + gl.Error}
	case !gl.OK:
		out := []string{" " + p.st.failed(g.Failed+" guard") + "  " + p.st.failed("refusing to run:") +
			fmt.Sprintf(" %d of %s outside %s", len(gl.Offenders), plural(gl.Hosts, "host"), cidrs)}
		for _, o := range gl.Offenders {
			out = append(out, "     "+o)
		}
		return out
	}
	return []string{" " + p.st.ok(g.OK) + " " + p.st.structure("guard") + "  " + fmt.Sprintf("%s in %s", plural(gl.Hosts, "host"), cidrs)}
}

// maxListedFiles is how many affected files a warning lists before "+N".
const maxListedFiles = 3

// warnings prints deduplicated warnings, fix arrows aligned.
func (p *Plain) warnings(groups []warnings.Group) []string {
	var visible []warnings.Group
	withFix := false
	for _, g := range groups {
		if !g.Hidden {
			visible = append(visible, g)
			withFix = withFix || g.Fix != ""
		}
	}
	if len(visible) == 0 {
		return nil
	}
	title := "Warnings (deduplicated)"
	if withFix {
		title = "To fix (deduplicated)"
	}
	out := []string{"", " " + p.st.changed(p.st.Glyphs.Warn+" "+title)}

	tagW, lefts := 0, make([]string, len(visible))
	for i, g := range visible {
		tagW = max(tagW, ansi.StringWidth(warnTag(g)))
		lefts[i] = g.Label + " " + p.st.changed(fmt.Sprintf("×%d", g.Count))
		if scope := warnScope(g); scope != "" {
			lefts[i] += p.st.meta(" · " + scope)
		}
	}
	// Align the arrows on the longest left part that leaves room for a fix.
	indent := 3 + tagW + 2
	leftW := 0
	for _, l := range lefts {
		if w := ansi.StringWidth(l); w <= p.layout()/2 {
			leftW = max(leftW, w)
		}
	}

	for i, g := range visible {
		line := "   " + p.st.structure(pad(warnTag(g), tagW)) + "  " + lefts[i]
		if g.Fix != "" {
			fix := p.st.ok("→ " + g.Fix)
			aligned := pad(line, indent+leftW) + "  " + fix
			if ansi.StringWidth(aligned) <= p.layout() {
				line = aligned
			} else {
				// No room: the fix goes on its own line rather than being cut.
				out = append(out, line)
				line = strings.Repeat(" ", indent) + fix
			}
		}
		out = append(out, line)
		if len(g.Locations) > 1 {
			files := g.Locations
			more := ""
			if len(files) > maxListedFiles {
				more = fmt.Sprintf(", +%d", len(files)-maxListedFiles)
				files = files[:maxListedFiles]
			}
			out = append(out, "   "+strings.Repeat(" ", tagW)+"  "+p.st.meta(strings.Join(files, ", ")+more))
		}
	}
	return out
}

func warnTag(g warnings.Group) string {
	if g.Tag != "" {
		return g.Tag
	}
	if g.Kind == warnings.KindDeprecation {
		return "deprec"
	}
	return "warn"
}

// warnScope says where a warning applies: files for deprecations (that is
// where the fix goes), hosts first for other warnings.
func warnScope(g warnings.Group) string {
	if g.Kind == warnings.KindDeprecation {
		switch {
		case len(g.Locations) == 1:
			return g.Locations[0]
		case len(g.Locations) > 1:
			return plural(len(g.Locations), "file")
		}
	}
	switch {
	case len(g.Hosts) == 1:
		return g.Hosts[0]
	case len(g.Hosts) > 1:
		return plural(len(g.Hosts), "host")
	case len(g.Locations) == 1:
		return g.Locations[0]
	case len(g.Locations) > 1:
		return plural(len(g.Locations), "file")
	}
	return ""
}

func (p *Plain) hostTable(hosts []agg.HostRecap) []string {
	type col struct {
		name  string
		value func(agg.HostRecap) int
		paint func(string) string
	}
	cols := []col{
		{"ok", func(h agg.HostRecap) int { return h.OK }, p.st.ok},
		{"chg", func(h agg.HostRecap) int { return h.Changed }, p.st.changed},
		{"skip", func(h agg.HostRecap) int { return h.Skipped }, p.st.meta},
		{"fail", func(h agg.HostRecap) int { return h.Failed }, p.st.failed},
		{"unreach", func(h agg.HostRecap) int { return h.Unreachable }, p.st.failed},
	}
	// Optional columns appear only when used.
	for _, c := range []col{
		{"rescued", func(h agg.HostRecap) int { return h.Rescued }, p.st.meta},
		{"ignored", func(h agg.HostRecap) int { return h.Ignored }, p.st.meta},
	} {
		for _, h := range hosts {
			if c.value(h) > 0 {
				cols = append(cols, c)
				break
			}
		}
	}

	hostW := len("host")
	for _, h := range hosts {
		hostW = max(hostW, ansi.StringWidth(h.Host))
	}
	head := "   " + pad("host", hostW)
	for _, c := range cols {
		head += "  " + pad(c.name, max(len(c.name), 3))
	}
	out := []string{p.st.meta(strings.TrimRight(head, " "))}
	for _, h := range hosts {
		row := "   " + pad(h.Host, hostW)
		for i, c := range cols {
			n := c.value(h)
			cell := strconv.Itoa(n)
			if i < len(cols)-1 { // no trailing spaces: they would sit inside color codes
				cell = pad(cell, max(len(c.name), 3))
			}
			if n > 0 {
				cell = c.paint(cell)
			} else {
				cell = p.st.meta(cell)
			}
			row += "  " + cell
		}
		out = append(out, strings.TrimRight(row, " "))
	}
	return out
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "<1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		d = d.Round(time.Second)
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
