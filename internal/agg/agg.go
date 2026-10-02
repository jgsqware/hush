// Package agg turns the event stream into a short list of things worth
// showing: changed tasks grouped across hosts, failures as they happen,
// debug output, per-playbook recaps and a final summary.
//
// The Aggregator is pure: no I/O, no clock. Feed it events with Handle and
// print the returned items; call Finish at the end of the stream.
package agg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jgsqware/hush/internal/events"
	"github.com/jgsqware/hush/internal/warnings"
)

// maxLabels is how many changed loop labels a TaskLine lists.
const maxLabels = 3

// stderrTail is how many stderr lines are kept to explain a failure that no
// task reported (syntax error, missing playbook...).
const stderrTail = 20

// Options tune what the Aggregator reports.
type Options struct {
	// Verbose also reports tasks that were ok on every host.
	Verbose bool
	// Argv is the ansible-playbook command line, used for rerun hints.
	// Nil in replay mode: the hint is then just the --start-at-task flag.
	Argv []string
	// LogPath is shown in the summary.
	LogPath string
	// Fixes is the fix table for warnings; nil uses the embedded one.
	Fixes *warnings.Table
}

// Aggregator consumes events and produces Items.
type Aggregator struct {
	opts Options

	pb   *playbook
	play *play
	// open tasks by uuid; with the linear strategy at most one is open.
	open    map[string]*task
	order   []string // open task uuids in start order
	current string   // uuid of the task that last started on a host

	argv        []string // current ansible-playbook command, for rerun hints
	interrupted bool
	notRun      int

	// whole run
	playbooks int
	exitCode  int
	hosts     []string
	recap     map[string]*HostRecap
	stderr    []string // last non-warning lines of the child's stderr
	warnParse warnings.Parser
	warns     *warnings.Collector
	// early holds warnings seen before the first playbook_start (stderr is
	// not synchronized with events): they belong to the coming playbook.
	early     []warnings.Warning
	first     time.Time
	last      time.Time
	durations time.Duration // sum of playbook durations, used when wall time is unknown
}

type playbook struct {
	name        string
	base        string // absolute playbook dir prefix, stripped from task paths
	hosts       []string
	check       bool
	headerShown bool
	counts      TaskCounts
	start, last time.Time
	duration    time.Duration // from playbook_end when present
	exitCode    *int
	stats       map[string]events.HostStats
	live        map[string]*events.HostStats // counted from results, for when stats never comes
	failed      bool                         // a Failure was reported for this playbook
}

type play struct {
	free  bool
	hosts []string
	dead  map[string]bool // failed or unreachable: no more results expected
}

type task struct {
	uuid     string
	name     string
	action   string
	location string
	handler  bool
	results  map[string]*hostResult
	hosts    []string // in reporting order
	started  []string // hosts that began the task, for the live status
}

type hostResult struct {
	status      Status
	msg         string
	debug       json.RawMessage
	items       int
	itemsDone   int
	labels      []string // changed item labels
	failedItems []string
}

// New returns an Aggregator.
func New(opts Options) *Aggregator {
	return &Aggregator{
		opts:  opts,
		argv:  opts.Argv,
		open:  map[string]*task{},
		recap: map[string]*HostRecap{},
		warns: warnings.NewCollector(opts.Fixes),
	}
}

// Handle consumes one event and returns the items to print now.
func (a *Aggregator) Handle(e events.Event) []Item {
	var out []Item
	if !e.TS.IsZero() {
		if a.first.IsZero() {
			a.first = e.TS
		}
		a.last = e.TS
		if a.pb != nil {
			a.pb.last = e.TS
		}
	}

	switch e.Type {
	case events.PlaybookStart:
		a.flushWarnings()
		out = append(out, a.endPlaybook()...)
		a.startPlaybook(e)

	case events.PlayStart:
		out = append(out, a.closeAll()...)
		if a.pb == nil {
			a.startPlaybook(events.Event{TS: e.TS})
		}
		a.play = &play{free: isFree(e.Play.Strategy), hosts: e.Play.Hosts, dead: map[string]bool{}}
		for _, h := range e.Play.Hosts {
			a.pb.hosts = appendUnique(a.pb.hosts, h)
		}
		if e.Play.CheckMode {
			a.pb.check = true
		}

	case events.TaskStart:
		if a.play == nil || !a.play.free {
			out = append(out, a.closeAll()...)
		}
		if e.Task != nil && a.open[e.Task.UUID] == nil {
			a.open[e.Task.UUID] = &task{
				uuid:     e.Task.UUID,
				name:     e.Task.Name,
				action:   e.Task.Action,
				location: a.relLocation(e.Task),
				handler:  e.Task.IsHandler,
				results:  map[string]*hostResult{},
			}
			a.order = append(a.order, e.Task.UUID)
		}
		if e.Task != nil {
			a.current = e.Task.UUID
		}

	case events.RunnerStart:
		if t := a.open[e.TaskUUID]; t != nil {
			t.started = appendUnique(t.started, e.Host)
		}
		a.current = e.TaskUUID

	case events.ItemOK, events.ItemSkipped, events.ItemFailed:
		if t := a.open[e.TaskUUID]; t != nil && e.Item != nil {
			r := t.result(e.Host)
			r.items++
			switch {
			case e.Type == events.ItemFailed:
				label := e.Item.LabelText()
				if msg := rawText(e.Item.Msg); msg != "" {
					label += ": " + msg
				}
				r.failedItems = append(r.failedItems, label)
			case e.Item.Changed:
				r.itemsDone++
				r.labels = append(r.labels, e.Item.LabelText())
			}
		}

	case events.RunnerOK, events.RunnerSkipped, events.RunnerFailed, events.RunnerUnreachable:
		out = append(out, a.runnerResult(e)...)

	case events.NoHostsMatched:
		out = append(out, a.visible(Notice{Text: "no hosts matched"})...)

	case events.Stats:
		out = append(out, a.closeAll()...)
		if a.pb != nil {
			a.pb.stats = e.Stats
		}

	case events.Command:
		a.argv = e.Argv

	case events.Section:
		if s := e.SectionInfo; s != nil {
			out = append(out, a.endPlaybook()...)
			out = append(out, SectionHeader{Index: s.Index, Count: s.Count, Name: s.Name})
		}

	case events.Hook:
		if h := e.HookResult; h != nil {
			if h.ExitCode != 0 {
				a.fail(h.ExitCode)
			}
			out = append(out, HookLine{
				Name: h.Name, ExitCode: h.ExitCode, Duration: time.Duration(h.DurationMS) * time.Millisecond,
				Summary: h.Summary, Warnings: h.Warnings, Tail: h.Tail,
			})
		}

	case events.Guard:
		if g := e.GuardResult; g != nil {
			if !g.OK {
				a.fail(1)
			}
			out = append(out, GuardLine{OK: g.OK, Hosts: g.Hosts, CIDRs: g.CIDRs, Offenders: g.Offenders, Error: g.Error})
		}

	case events.NotRun:
		out = append(out, a.endPlaybook()...)
		if n := len(e.Skipped); n > 0 {
			a.notRun += n
			what := "1 playbook not run"
			if n > 1 {
				what = fmt.Sprintf("%d playbooks not run", n)
			}
			out = append(out, Notice{Text: what + " (" + e.Reason + ")"})
		}

	case events.Stderr:
		w, other := a.warnParse.Line(e.Line)
		if w != nil {
			a.addWarning(*w)
		}
		if other {
			a.keepStderr(e.Line)
		}

	case events.Warning, events.Deprecation:
		w := warnings.Warning{Kind: e.Type, Msg: e.Msg, Origin: e.Origin, Help: e.Help, Version: e.Version, Host: e.Host}
		warnings.Normalize(&w)
		a.addWarning(w)

	case events.PlaybookEnd:
		a.flushWarnings()
		if e.Interrupted {
			a.interrupted = true
		}
		if a.pb == nil {
			// ansible-playbook died before starting the playbook.
			if e.ExitCode != nil && *e.ExitCode != 0 {
				if a.exitCode == 0 {
					a.exitCode = *e.ExitCode
				}
				out = append(out, a.unexplained(*e.ExitCode))
			}
			break
		}
		a.pb.exitCode = e.ExitCode
		a.pb.duration = time.Duration(e.DurationMS) * time.Millisecond
		out = append(out, a.endPlaybook()...)
	}
	return out
}

// Activity is what the run is doing right now, for live status lines.
type Activity struct {
	Playbook string
	Task     string
	Pending  []string // hosts running a task that have not reported yet
}

// Activity returns what the run is doing right now.
func (a *Aggregator) Activity() Activity {
	var s Activity
	if a.pb != nil {
		s.Playbook = a.pb.name
	}
	if t := a.open[a.current]; t != nil {
		s.Task = t.name
	}
	for _, uuid := range a.order {
		t := a.open[uuid]
		for _, h := range t.started {
			if _, done := t.results[h]; !done {
				s.Pending = appendUnique(s.Pending, h)
			}
		}
	}
	return s
}

// Finish flushes what is pending and returns the closing items, ending with
// the Summary.
func (a *Aggregator) Finish() []Item {
	a.flushWarnings()
	a.addEarly("")
	out := a.endPlaybook()
	return append(out, a.summary())
}

// flushWarnings completes a pending stderr warning block, while the
// playbook it belongs to is still current.
func (a *Aggregator) flushWarnings() {
	if w := a.warnParse.Flush(); w != nil {
		a.addWarning(*w)
	}
}

func (a *Aggregator) addWarning(w warnings.Warning) {
	if a.pb == nil {
		a.early = append(a.early, w)
		return
	}
	a.warns.Add(w, a.pb.base)
}

// addEarly files warnings that arrived before a playbook under base.
func (a *Aggregator) addEarly(base string) {
	for _, w := range a.early {
		a.warns.Add(w, base)
	}
	a.early = nil
}

func isDebug(action string) bool {
	return action == "debug" || strings.HasSuffix(action, ".debug")
}

func isFree(strategy string) bool {
	return strategy == "free" || strategy == "host_pinned"
}

func (a *Aggregator) startPlaybook(e events.Event) {
	pb := &playbook{start: e.TS, last: e.TS, live: map[string]*events.HostStats{}}
	if e.Playbook != nil {
		pb.name = e.Playbook.Rel
		if strings.HasSuffix(e.Playbook.Path, e.Playbook.Rel) {
			pb.base = strings.TrimSuffix(e.Playbook.Path, e.Playbook.Rel)
		}
	}
	if e.Options != nil && e.Options.Check {
		pb.check = true
	}
	a.pb = pb
	a.play = nil
	a.playbooks++
	a.addEarly(pb.base)
}

func (a *Aggregator) relLocation(t *events.Task) string {
	loc := t.Location()
	if a.pb != nil && a.pb.base != "" {
		loc = strings.TrimPrefix(loc, a.pb.base)
	}
	return loc
}

func (t *task) result(host string) *hostResult {
	r := t.results[host]
	if r == nil {
		r = &hostResult{}
		t.results[host] = r
		t.hosts = append(t.hosts, host)
	}
	return r
}

func (a *Aggregator) runnerResult(e events.Event) []Item {
	t := a.open[e.TaskUUID]
	if t == nil || e.Result == nil {
		return nil
	}
	res := e.Result
	r := t.result(e.Host)
	r.msg = res.MsgText()
	r.debug = res.Debug
	if len(r.debug) == 0 && isDebug(t.action) {
		r.debug = res.Msg // streams recorded before the plugin had a debug field
	}

	a.countLive(e)

	var out []Item
	switch e.Type {
	case events.RunnerOK:
		r.status = StatusOK
		if res.Changed {
			r.status = StatusChanged
		}
	case events.RunnerSkipped:
		r.status = StatusSkipped
	case events.RunnerFailed:
		switch {
		case e.IgnoreErrors:
			r.status = StatusIgnored
		case res.Rescuable:
			r.status = StatusRescued
		default:
			r.status = StatusFailed
			a.markDead(e.Host)
			out = a.visible(a.failure(t, e, r))
			a.pb.failed = true
		}
	case events.RunnerUnreachable:
		r.status = StatusUnreachable
		a.markDead(e.Host)
		out = a.visible(a.failure(t, e, r))
		a.pb.failed = true
	}

	if a.play != nil && a.play.free && a.allReported(t) {
		out = append(out, a.close(t.uuid)...)
	}
	return out
}

// countLive keeps an Ansible-style recap from task results; an interrupted
// ansible-playbook never sends its stats.
func (a *Aggregator) countLive(e events.Event) {
	if a.pb == nil {
		return
	}
	s := a.pb.live[e.Host]
	if s == nil {
		s = &events.HostStats{}
		a.pb.live[e.Host] = s
	}
	switch e.Type {
	case events.RunnerOK:
		s.OK++
		if e.Result.Changed {
			s.Changed++
		}
	case events.RunnerSkipped:
		s.Skipped++
	case events.RunnerUnreachable:
		s.Unreachable++
	case events.RunnerFailed:
		switch {
		case e.IgnoreErrors:
			s.Ignored++
		case e.Result.Rescuable:
			s.Rescued++
		default:
			s.Failures++
		}
	}
}

func (a *Aggregator) markDead(host string) {
	if a.play != nil {
		a.play.dead[host] = true
	}
}

// allReported tells whether every live host of the play has a result for t.
func (a *Aggregator) allReported(t *task) bool {
	for _, h := range a.play.hosts {
		if _, ok := t.results[h]; !ok && !a.play.dead[h] {
			return false
		}
	}
	return true
}

func (a *Aggregator) failure(t *task, e events.Event, r *hostResult) Failure {
	f := Failure{
		Task:        t.name,
		Location:    t.location,
		Host:        e.Host,
		Unreachable: e.Type == events.RunnerUnreachable,
		Msg:         r.msg,
		RC:          e.Result.RC,
		Stdout:      e.Result.Stdout,
		StdoutTotal: e.Result.StdoutLinesTotal,
		Stderr:      e.Result.Stderr,
		StderrTotal: e.Result.StderrLinesTotal,
		FailedItems: r.failedItems,
	}
	if !f.Unreachable {
		f.Rerun = a.rerunHint(t.name)
	}
	return f
}

// rerunHint returns the command line to resume at the given task.
func (a *Aggregator) rerunHint(name string) string {
	flag := "--start-at-task " + shellQuote(name)
	if len(a.argv) == 0 {
		return flag
	}
	var args []string
	for i := 0; i < len(a.argv); i++ {
		arg := a.argv[i]
		if arg == "--start-at-task" {
			i++ // drop its value too
			continue
		}
		if strings.HasPrefix(arg, "--start-at-task=") {
			continue
		}
		args = append(args, shellQuote(arg))
	}
	return strings.Join(args, " ") + " " + flag
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, needsQuote) < 0 {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

func needsQuote(r rune) bool {
	safe := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,@%+", r)
	return !safe
}

// visible prefixes items with the playbook header when it was not shown yet.
func (a *Aggregator) visible(items ...Item) []Item {
	if a.pb == nil || a.pb.headerShown {
		return items
	}
	a.pb.headerShown = true
	h := PlaybookHeader{Name: a.pb.name, Hosts: a.pb.hosts, Check: a.pb.check}
	return append([]Item{h}, items...)
}

func (a *Aggregator) closeAll() []Item {
	var out []Item
	for len(a.order) > 0 {
		out = append(out, a.close(a.order[0])...)
	}
	return out
}

// close removes a task from the open set, counts it and returns what to show.
func (a *Aggregator) close(uuid string) []Item {
	t := a.open[uuid]
	delete(a.open, uuid)
	for i, u := range a.order {
		if u == uuid {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
	if t == nil || len(t.hosts) == 0 {
		return nil // meta tasks, or a task no host ran
	}

	groups := groupHosts(t)
	dominant := groups[0].Status
	if a.pb != nil {
		a.pb.counts.add(worst(groups))
	}

	if boxes := a.infoBoxes(t); len(boxes) > 0 {
		return a.visible(boxes...)
	}

	show := a.opts.Verbose
	for _, g := range groups {
		if g.Status == StatusChanged || g.Status == StatusIgnored || g.Status == StatusRescued {
			show = true
		}
	}
	// A failure alone was already reported; show the line only if hosts diverge.
	if (dominant == StatusFailed || dominant == StatusUnreachable) && len(groups) == 1 {
		show = false
	}
	if !show {
		return nil
	}

	line := TaskLine{
		Name:     t.name,
		Location: t.location,
		Handler:  t.handler,
		Status:   dominant,
		Groups:   groups,
		Loop:     loopSummary(t),
		OneHost:  a.pb != nil && len(a.pb.hosts) == 1,
	}
	if dominant == StatusRescued || dominant == StatusIgnored {
		line.Note = t.results[groups[0].Hosts[0]].msg
	}
	return a.visible(line)
}

// displayRank orders host groups on a task line. Failures come after changes
// because they were already reported in full when they happened.
var displayRank = map[Status]int{
	StatusChanged: 0, StatusRescued: 1, StatusIgnored: 2, StatusFailed: 3,
	StatusUnreachable: 4, StatusOK: 5, StatusSkipped: 6,
}

// worst returns the status a task is counted under in the playbook recap.
func worst(groups []HostGroup) Status {
	order := []Status{StatusUnreachable, StatusFailed, StatusChanged, StatusRescued, StatusIgnored, StatusOK, StatusSkipped}
	for _, st := range order {
		for _, g := range groups {
			if g.Status == st {
				return st
			}
		}
	}
	return StatusOK
}

func groupHosts(t *task) []HostGroup {
	idx := map[Status]int{}
	var groups []HostGroup
	for _, h := range t.hosts {
		s := t.results[h].status
		i, ok := idx[s]
		if !ok {
			i = len(groups)
			idx[s] = i
			groups = append(groups, HostGroup{Status: s})
		}
		groups[i].Hosts = append(groups[i].Hosts, h)
	}
	sort.SliceStable(groups, func(i, j int) bool { return displayRank[groups[i].Status] < displayRank[groups[j].Status] })
	return groups
}

func loopSummary(t *task) *Loop {
	var l Loop
	ratios := map[string]bool{}
	seen := map[string]bool{}
	for _, h := range t.hosts {
		r := t.results[h]
		if r.status != StatusChanged || r.items == 0 {
			continue
		}
		l.Changed += r.itemsDone
		l.Total += r.items
		ratio := fmt.Sprintf("%d/%d", r.itemsDone, r.items)
		ratios[ratio] = true
		l.PerHost = append(l.PerHost, h+" "+ratio)
		for _, lb := range r.labels {
			if seen[lb] {
				continue
			}
			seen[lb] = true
			if len(l.Labels) < maxLabels {
				l.Labels = append(l.Labels, lb)
			} else {
				l.MoreLabels++
			}
		}
	}
	if l.Total == 0 {
		return nil
	}
	if len(ratios) == 1 {
		// Hosts agree: report one host's ratio, not the sum.
		l.PerHost = nil
		for r := range ratios {
			_, _ = fmt.Sscanf(r, "%d/%d", &l.Changed, &l.Total)
		}
	}
	return &l
}

// infoBoxes renders debug output, one box per distinct output.
func (a *Aggregator) infoBoxes(t *task) []Item {
	var boxes []Item
	index := map[string]int{}
	for _, h := range t.hosts {
		r := t.results[h]
		if len(r.debug) == 0 || r.status == StatusSkipped {
			continue
		}
		key := string(r.debug)
		i, ok := index[key]
		if !ok {
			i = len(boxes)
			index[key] = i
			boxes = append(boxes, InfoBox{Task: t.name, Lines: debugLines(r.debug)})
		}
		b := boxes[i].(InfoBox)
		b.Hosts = append(b.Hosts, h)
		boxes[i] = b
	}
	if len(boxes) == 1 {
		b := boxes[0].(InfoBox)
		b.AllHosts = len(b.Hosts) == len(t.hosts)
		boxes[0] = b
	}
	return boxes
}

// debugLines flattens debug output: strings split on newlines, lists one
// element per line, objects as "key: value".
func debugLines(raw json.RawMessage) []string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return []string{string(raw)}
	}
	var lines []string
	switch x := v.(type) {
	case string:
		lines = strings.Split(strings.TrimRight(x, "\n"), "\n")
	case []any:
		for _, el := range x {
			lines = append(lines, strings.Split(scalar(el), "\n")...)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, k+": "+scalar(x[k]))
		}
	default:
		lines = []string{scalar(x)}
	}
	return lines
}

func scalar(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return scalar(json.RawMessage(raw))
}

func (c *TaskCounts) add(s Status) {
	switch s {
	case StatusOK:
		c.OK++
	case StatusChanged:
		c.Changed++
	case StatusSkipped:
		c.Skipped++
	case StatusIgnored:
		c.Ignored++
	case StatusRescued:
		c.Rescued++
	default:
		c.Failed++
	}
}

// endPlaybook closes the current playbook, if any, and returns its recap.
func (a *Aggregator) endPlaybook() []Item {
	if a.pb == nil {
		return nil
	}
	out := a.closeAll()
	pb := a.pb
	a.pb, a.play = nil, nil

	duration := pb.duration
	if duration == 0 && !pb.start.IsZero() {
		duration = pb.last.Sub(pb.start)
	}
	a.durations += duration

	code := 0
	switch {
	case pb.exitCode != nil:
		code = *pb.exitCode
	default:
		code = pb.inferExitCode()
	}
	if code != 0 && a.exitCode == 0 {
		a.exitCode = code
	}
	if code != 0 && !pb.failed && !a.interrupted { // an interruption explains itself
		a.pb = pb // so the header is shown
		out = append(out, a.visible(a.unexplained(code))...)
		a.pb = nil
	}

	for _, h := range pb.hosts {
		a.hosts = appendUnique(a.hosts, h)
	}
	stats := pb.stats
	if stats == nil {
		stats = map[string]events.HostStats{}
		for h, s := range pb.live {
			stats[h] = *s
		}
	}
	for h, s := range stats {
		a.hosts = appendUnique(a.hosts, h)
		r := a.recap[h]
		if r == nil {
			r = &HostRecap{Host: h}
			a.recap[h] = r
		}
		r.OK += s.OK
		r.Changed += s.Changed
		r.Skipped += s.Skipped
		r.Failed += s.Failures
		r.Unreachable += s.Unreachable
		r.Rescued += s.Rescued
		r.Ignored += s.Ignored
	}

	return append(out, PlaybookRecap{
		Name:        pb.name,
		Hosts:       pb.hosts,
		Counts:      pb.counts,
		Duration:    duration,
		ExitCode:    code,
		HeaderShown: pb.headerShown,
		Check:       pb.check,
	})
}

// inferExitCode mimics ansible-playbook when the stream has no playbook_end
// (recorded without hush): 4 if a host was unreachable, 2 on failures.
// keepStderr remembers the last non-blank stderr lines.
func (a *Aggregator) keepStderr(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	a.stderr = append(a.stderr, line)
	if len(a.stderr) > stderrTail {
		a.stderr = a.stderr[len(a.stderr)-stderrTail:]
	}
}

// unexplained reports a non-zero exit that no task failure accounts for.
func (a *Aggregator) unexplained(code int) Failure {
	msg := strings.Join(a.stderr, "\n")
	if msg == "" {
		msg = "no error output; see the log"
	}
	return Failure{Task: fmt.Sprintf("ansible-playbook exited with code %d", code), Msg: msg}
}

func (pb *playbook) inferExitCode() int {
	code := 0
	for _, s := range pb.stats {
		if s.Unreachable > 0 {
			return 4
		}
		if s.Failures > 0 {
			code = 2
		}
	}
	return code
}

// fail records a failure exit code, keeping the first one.
func (a *Aggregator) fail(code int) {
	if a.exitCode == 0 {
		a.exitCode = code
	}
}

func (a *Aggregator) summary() Summary {
	s := Summary{
		ExitCode:  a.exitCode,
		NotRun:    a.notRun,
		Playbooks: a.playbooks,
		LogPath:   a.opts.LogPath,
		Duration:  a.last.Sub(a.first),
		Warnings:  a.warns.Groups(),
	}
	if s.Duration <= 0 {
		s.Duration = a.durations
	}
	switch {
	case a.interrupted:
		s.Verdict, s.ExitCode = VerdictInterrupted, 130
	case s.ExitCode != 0:
		s.Verdict = VerdictFailed
	}
	for _, h := range a.hosts {
		r, ok := a.recap[h]
		if !ok {
			r = &HostRecap{Host: h}
		}
		s.Hosts = append(s.Hosts, *r)
		s.Changed += r.Changed
		s.Failed += r.Failed + r.Unreachable
	}
	return s
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
