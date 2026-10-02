package agg

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jgsqware/hush/internal/events"
)

// stream builds synthetic event streams.
type stream struct {
	evs []events.Event
	t   time.Time
}

func newStream() *stream {
	s := &stream{t: time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)}
	s.add(events.Event{Type: events.PlaybookStart, Playbook: &events.Playbook{Path: "/w/site.yaml", Rel: "site.yaml"}})
	return s
}

func (s *stream) add(e events.Event) *stream {
	s.t = s.t.Add(time.Second)
	e.TS = s.t
	s.evs = append(s.evs, e)
	return s
}

func (s *stream) play(strategy string, hosts ...string) *stream {
	return s.add(events.Event{Type: events.PlayStart, Play: &events.Play{UUID: "p", Name: "p", Hosts: hosts, Strategy: strategy}})
}

func (s *stream) task(uuid, name string) *stream {
	return s.add(events.Event{Type: events.TaskStart, Task: &events.Task{UUID: uuid, Name: name, Action: "command", Path: "/w/site.yaml", Line: 3}})
}

func (s *stream) handler(uuid, name string) *stream {
	return s.add(events.Event{Type: events.TaskStart, Task: &events.Task{UUID: uuid, Name: name, IsHandler: true}})
}

func (s *stream) result(typ, uuid, host string, r events.Result, mod ...func(*events.Event)) *stream {
	e := events.Event{Type: typ, TaskUUID: uuid, Host: host, Result: &r}
	for _, m := range mod {
		m(&e)
	}
	return s.add(e)
}

func (s *stream) ok(uuid string, hosts ...string) *stream {
	for _, h := range hosts {
		s.result(events.RunnerOK, uuid, h, events.Result{})
	}
	return s
}

func (s *stream) changed(uuid string, hosts ...string) *stream {
	for _, h := range hosts {
		s.result(events.RunnerOK, uuid, h, events.Result{Changed: true})
	}
	return s
}

func (s *stream) skipped(uuid string, hosts ...string) *stream {
	for _, h := range hosts {
		s.result(events.RunnerSkipped, uuid, h, events.Result{})
	}
	return s
}

func (s *stream) item(uuid, host, label string, changed bool) *stream {
	return s.add(events.Event{Type: events.ItemOK, TaskUUID: uuid, Host: host,
		Item: &events.Item{Label: json.RawMessage(`"` + label + `"`), Changed: changed}})
}

func (s *stream) stats(per map[string]events.HostStats) *stream {
	return s.add(events.Event{Type: events.Stats, Stats: per})
}

// run feeds the stream and returns all items, Summary included.
func run(opts Options, s *stream) []Item {
	a := New(opts)
	var out []Item
	for _, e := range s.evs {
		out = append(out, a.Handle(e)...)
	}
	return append(out, a.Finish()...)
}

func only[T Item](items []Item) []T {
	var out []T
	for _, it := range items {
		if v, ok := it.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func lastOf[T Item](t *testing.T, items []Item) T {
	t.Helper()
	all := only[T](items)
	if len(all) == 0 {
		var zero T
		t.Fatalf("no %T in %#v", zero, items)
	}
	return all[len(all)-1]
}

func TestOKAndSkippedPrintNothing(t *testing.T) {
	s := newStream().play("linear", "a", "b").
		task("t1", "ok").ok("t1", "a", "b").
		task("t2", "skip").skipped("t2", "a", "b")
	items := run(Options{}, s)

	if n := len(only[TaskLine](items)); n != 0 {
		t.Fatalf("got %d task lines, want 0", n)
	}
	if n := len(only[PlaybookHeader](items)); n != 0 {
		t.Fatalf("header shown for a quiet playbook")
	}
	r := lastOf[PlaybookRecap](t, items)
	if r.HeaderShown || r.Counts != (TaskCounts{OK: 1, Skipped: 1}) {
		t.Fatalf("recap = %+v", r)
	}
}

func TestVerboseShowsOK(t *testing.T) {
	s := newStream().play("linear", "a").task("t1", "ok").ok("t1", "a")
	if n := len(only[TaskLine](run(Options{Verbose: true}, s))); n != 1 {
		t.Fatalf("got %d task lines, want 1", n)
	}
}

func TestChangedGroupedAcrossHosts(t *testing.T) {
	s := newStream().play("linear", "a", "b").task("t1", "Restart caddy").changed("t1", "a", "b").task("t2", "next")
	items := run(Options{}, s)
	// The header must come right before the first visible line.
	if _, ok := items[0].(PlaybookHeader); !ok {
		t.Fatalf("first item = %T, want PlaybookHeader", items[0])
	}
	line := lastOf[TaskLine](t, items)
	want := []HostGroup{{Status: StatusChanged, Hosts: []string{"a", "b"}}}
	if !reflect.DeepEqual(line.Groups, want) || line.Location != "site.yaml:3" {
		t.Fatalf("line = %+v", line)
	}
}

func TestDivergentHosts(t *testing.T) {
	s := newStream().play("linear", "a", "b", "c").task("t1", "x").
		changed("t1", "a").skipped("t1", "b").ok("t1", "c")
	line := lastOf[TaskLine](t, run(Options{}, s))
	want := []HostGroup{
		{Status: StatusChanged, Hosts: []string{"a"}},
		{Status: StatusOK, Hosts: []string{"c"}},
		{Status: StatusSkipped, Hosts: []string{"b"}},
	}
	if !reflect.DeepEqual(line.Groups, want) {
		t.Fatalf("groups = %+v", line.Groups)
	}
}

func TestLinearClosesOnNextTask(t *testing.T) {
	s := newStream().play("linear", "a").task("t1", "x").changed("t1", "a")
	a := New(Options{})
	for _, e := range s.evs {
		if got := a.Handle(e); len(got) != 0 {
			t.Fatalf("emitted %#v before the task closed", got)
		}
	}
	got := a.Handle(events.Event{Type: events.TaskStart, Task: &events.Task{UUID: "t2", Name: "y"}})
	if len(only[TaskLine](got)) != 1 {
		t.Fatalf("task not flushed on next task_start: %#v", got)
	}
}

func TestFreeStrategy(t *testing.T) {
	// Free: task_start once per host, interleaved results; a task closes once
	// every live host has reported.
	s := newStream().play("free", "a", "b").
		task("t1", "one").task("t1", "one").
		changed("t1", "b").
		task("t2", "two").ok("t2", "b")
	a := New(Options{})
	var out []Item
	for _, e := range s.evs {
		out = append(out, a.Handle(e)...)
	}
	if len(only[TaskLine](out)) != 0 {
		t.Fatalf("t1 closed before host a reported")
	}
	got := a.Handle(events.Event{Type: events.RunnerOK, TaskUUID: "t1", Host: "a", Result: &events.Result{Changed: true}})
	line := lastOf[TaskLine](t, got)
	if line.Name != "one" || len(line.Groups) != 1 || len(line.Groups[0].Hosts) != 2 {
		t.Fatalf("line = %+v", line)
	}
}

func TestFreeStrategyIgnoresDeadHosts(t *testing.T) {
	s := newStream().play("free", "a", "b").
		task("t1", "one").
		result(events.RunnerUnreachable, "t1", "a", events.Result{Unreachable: true, Msg: json.RawMessage(`"down"`)}).
		changed("t1", "b").
		task("t2", "two").changed("t2", "b")
	a := New(Options{})
	var lines []TaskLine
	for _, e := range s.evs {
		lines = append(lines, only[TaskLine](a.Handle(e))...)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines before play end, want 2 (dead host must not block)", len(lines))
	}
}

func TestFailureReportedImmediately(t *testing.T) {
	s := newStream().play("linear", "a", "b").task("t1", "Restart caddy").
		result(events.RunnerFailed, "t1", "b", events.Result{Failed: true, Changed: true, Msg: json.RawMessage(`"boom"`), Stderr: "e1\ne2", StderrLinesTotal: 40})
	a := New(Options{Argv: []string{"ansible-playbook", "site.yml", "--start-at-task", "old", "--limit", "web,db"}})
	var got []Item
	for _, e := range s.evs {
		got = a.Handle(e)
	}
	f := lastOf[Failure](t, got)
	want := Failure{
		Task: "Restart caddy", Location: "site.yaml:3", Host: "b", Msg: "boom",
		Stderr: "e1\ne2", StderrTotal: 40,
		Rerun: `ansible-playbook site.yml --limit web,db --start-at-task "Restart caddy"`,
	}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("failure =\n%+v\nwant\n%+v", f, want)
	}
}

func TestFailedOnlyTaskHasNoExtraLine(t *testing.T) {
	s := newStream().play("linear", "a").task("t1", "x").
		result(events.RunnerFailed, "t1", "a", events.Result{Failed: true, Changed: true})
	items := run(Options{}, s)
	if len(only[TaskLine](items)) != 0 {
		t.Fatal("failure printed twice")
	}
	if c := lastOf[PlaybookRecap](t, items).Counts; c.Failed != 1 || c.Changed != 0 {
		t.Fatalf("failed command counted as changed: %+v", c)
	}
}

func TestUnreachableMidPlay(t *testing.T) {
	s := newStream().play("linear", "a", "b").
		task("t1", "x").ok("t1", "a", "b").
		task("t2", "y").changed("t2", "a").
		result(events.RunnerUnreachable, "t2", "b", events.Result{Unreachable: true, Msg: json.RawMessage(`"Task failed: ssh down"`)}).
		task("t3", "z").changed("t3", "a").
		stats(map[string]events.HostStats{"a": {OK: 3, Changed: 2}, "b": {OK: 1, Unreachable: 1}})
	items := run(Options{}, s)
	f := lastOf[Failure](t, items)
	if !f.Unreachable || f.Msg != "ssh down" || f.Rerun != "" {
		t.Fatalf("failure = %+v", f)
	}
	sum := lastOf[Summary](t, items)
	if sum.Verdict != VerdictFailed || sum.ExitCode != 4 || sum.Failed != 1 || sum.Changed != 2 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestIgnoreErrors(t *testing.T) {
	s := newStream().play("linear", "a").task("t1", "x").
		result(events.RunnerFailed, "t1", "a", events.Result{Failed: true, Msg: json.RawMessage(`"meh"`)},
			func(e *events.Event) { e.IgnoreErrors = true })
	items := run(Options{}, s)
	if len(only[Failure](items)) != 0 {
		t.Fatal("ignored failure reported as failure")
	}
	line := lastOf[TaskLine](t, items)
	if line.Status != StatusIgnored || line.Note != "meh" {
		t.Fatalf("line = %+v", line)
	}
	if lastOf[Summary](t, items).Verdict != VerdictSuccess {
		t.Fatal("ignored failure fails the run")
	}
}

func TestRescue(t *testing.T) {
	s := newStream().play("linear", "a").task("t1", "x").
		result(events.RunnerFailed, "t1", "a", events.Result{Failed: true, Rescuable: true, Msg: json.RawMessage(`"oops"`)}).
		task("t2", "recover").ok("t2", "a")
	items := run(Options{}, s)
	if len(only[Failure](items)) != 0 {
		t.Fatal("rescued failure reported as failure")
	}
	if line := lastOf[TaskLine](t, items); line.Status != StatusRescued || line.Note != "oops" {
		t.Fatalf("line = %+v", line)
	}
}

func TestLoopSummary(t *testing.T) {
	s := newStream().play("linear", "a", "b").task("t1", "dirs")
	for _, h := range []string{"a", "b"} {
		s.item("t1", h, "conf.d", true).item("t1", h, "provisioning", true).
			item("t1", h, "dashboards", false).item("t1", h, "alerts", true)
	}
	s.changed("t1", "a", "b")
	line := lastOf[TaskLine](t, run(Options{}, s))
	want := &Loop{Changed: 3, Total: 4, Labels: []string{"conf.d", "provisioning", "alerts"}}
	if !reflect.DeepEqual(line.Loop, want) {
		t.Fatalf("loop = %+v", line.Loop)
	}
}

func TestLoopHostsDiffer(t *testing.T) {
	s := newStream().play("linear", "a", "b").task("t1", "dirs").
		item("t1", "a", "x", true).item("t1", "a", "y", true).
		item("t1", "b", "x", true).item("t1", "b", "y", false).
		changed("t1", "a", "b")
	l := lastOf[TaskLine](t, run(Options{}, s)).Loop
	if !reflect.DeepEqual(l.PerHost, []string{"a 2/2", "b 1/2"}) {
		t.Fatalf("per host = %v", l.PerHost)
	}
}

func TestHandlers(t *testing.T) {
	s := newStream().play("linear", "a").handler("h1", "Restart demo").changed("h1", "a")
	if line := lastOf[TaskLine](t, run(Options{}, s)); !line.Handler {
		t.Fatalf("line = %+v", line)
	}
}

func TestIncludeTaskIsQuiet(t *testing.T) {
	s := newStream().play("linear", "a").task("t1", "Include extra").ok("t1", "a").
		add(events.Event{Type: events.Include, File: "/w/extra.yaml", Hosts: []string{"a"}, TaskUUID: "t1"}).
		task("t2", "From include").changed("t2", "a")
	lines := only[TaskLine](run(Options{}, s))
	if len(lines) != 1 || lines[0].Name != "From include" {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestDebugInfoBox(t *testing.T) {
	s := newStream().play("linear", "a", "b").task("t1", "Show").
		result(events.RunnerOK, "t1", "a", events.Result{Debug: json.RawMessage(`["v1.17", "otlp"]`)}).
		result(events.RunnerOK, "t1", "b", events.Result{Debug: json.RawMessage(`["v1.17", "otlp"]`)}).
		task("t2", "Var").
		result(events.RunnerOK, "t2", "a", events.Result{Debug: json.RawMessage(`{"port": 4317}`)}).
		result(events.RunnerOK, "t2", "b", events.Result{Debug: json.RawMessage(`{"port": 4318}`)})
	boxes := only[InfoBox](run(Options{}, s))
	want := []InfoBox{
		{Task: "Show", Hosts: []string{"a", "b"}, Lines: []string{"v1.17", "otlp"}, AllHosts: true},
		{Task: "Var", Hosts: []string{"a"}, Lines: []string{"port: 4317"}},
		{Task: "Var", Hosts: []string{"b"}, Lines: []string{"port: 4318"}},
	}
	if !reflect.DeepEqual(boxes, want) {
		t.Fatalf("boxes =\n%+v\nwant\n%+v", boxes, want)
	}
}

func TestCheckMode(t *testing.T) {
	s := newStream()
	s.evs[0].Options = &events.Options{Check: true}
	s.play("linear", "a").task("t1", "x").changed("t1", "a")
	items := run(Options{}, s)
	if !lastOf[PlaybookHeader](t, items).Check || !lastOf[PlaybookRecap](t, items).Check {
		t.Fatal("check mode not reported")
	}
}

func TestExitCodeFromPlaybookEnd(t *testing.T) {
	code := 2
	s := newStream().play("linear", "a").
		add(events.Event{Type: events.PlaybookEnd, ExitCode: &code, DurationMS: 13000})
	items := run(Options{}, s)
	if r := lastOf[PlaybookRecap](t, items); r.ExitCode != 2 || r.Duration != 13*time.Second {
		t.Fatalf("recap = %+v", r)
	}
	if s := lastOf[Summary](t, items); s.Verdict != VerdictFailed || s.ExitCode != 2 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestMultiplePlaybooksSumRecap(t *testing.T) {
	s := newStream().play("linear", "a").stats(map[string]events.HostStats{"a": {OK: 3, Changed: 1}})
	s.add(events.Event{Type: events.PlaybookStart, Playbook: &events.Playbook{Rel: "two.yaml"}}).
		play("linear", "a", "b").stats(map[string]events.HostStats{"a": {OK: 2, Changed: 2}, "b": {OK: 1}})
	sum := lastOf[Summary](t, run(Options{}, s))
	want := []HostRecap{{Host: "a", OK: 5, Changed: 3}, {Host: "b", OK: 1}}
	if sum.Playbooks != 2 || !reflect.DeepEqual(sum.Hosts, want) || sum.Changed != 3 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"site.yml":      "site.yml",
		"web,db":        "web,db",
		"Restart caddy": `"Restart caddy"`,
		`say "hi" $x`:   `"say \"hi\" \$x"`,
		"":              `""`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func stderrLines(s *stream, lines ...string) *stream {
	for _, l := range lines {
		s.add(events.Event{Type: events.Stderr, Line: l})
	}
	return s
}

func TestWarningsFromStderrAndCallback(t *testing.T) {
	// stderr before playbook_start must still be relative to the playbook dir.
	s := &stream{t: time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)}
	stderrLines(s,
		"[DEPRECATION WARNING]: apt_key has been deprecated. Use deb822_repository instead. This feature will be removed from ansible-core version 2.25.",
		"Origin: /w/roles/docker.yaml:23:3",
		"",
	)
	s.add(events.Event{Type: events.PlaybookStart, Playbook: &events.Playbook{Path: "/w/site.yaml", Rel: "site.yaml"}})
	s.play("linear", "a", "b").task("t1", "copy").changed("t1", "a", "b")
	for _, h := range []string{"a", "b"} {
		s.add(events.Event{Type: events.Warning, Host: h, Msg: "sftp transfer mechanism failed on [10.0.0.1]. Use ANSIBLE_DEBUG=1"})
	}
	sum := lastOf[Summary](t, run(Options{}, s))

	if len(sum.Warnings) != 2 {
		t.Fatalf("warnings = %+v", sum.Warnings)
	}
	ssh, apt := sum.Warnings[0], sum.Warnings[1]
	if ssh.Key != "ssh_transfer_fallback" || ssh.Count != 2 || len(ssh.Hosts) != 2 {
		t.Errorf("ssh = %+v", ssh)
	}
	if apt.Key != "deprecation.apt_key" || !reflect.DeepEqual(apt.Locations, []string{"roles/docker.yaml:23"}) {
		t.Errorf("apt = %+v", apt)
	}
}

func TestWarningsDoNotExplainFailures(t *testing.T) {
	code := 1
	s := &stream{t: time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)}
	stderrLines(s,
		"[WARNING]: provided hosts list is empty, only localhost is available",
		"[ERROR]: the playbook: nope.yml could not be found",
	)
	s.add(events.Event{Type: events.PlaybookEnd, ExitCode: &code})
	f := lastOf[Failure](t, run(Options{}, s))
	if f.Msg != "[ERROR]: the playbook: nope.yml could not be found" {
		t.Fatalf("msg = %q", f.Msg)
	}
}

func TestActivity(t *testing.T) {
	s := newStream().play("linear", "a", "b").task("t1", "Restart caddy").
		add(events.Event{Type: events.RunnerStart, TaskUUID: "t1", Host: "a"}).
		add(events.Event{Type: events.RunnerStart, TaskUUID: "t1", Host: "b"}).
		changed("t1", "a")
	a := New(Options{})
	for _, e := range s.evs {
		a.Handle(e)
	}
	got := a.Activity()
	want := Activity{Playbook: "site.yaml", Task: "Restart caddy", Pending: []string{"b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activity = %+v, want %+v", got, want)
	}
}

func TestInterruptedRecapFromResults(t *testing.T) {
	code := 99
	s := newStream().play("linear", "a", "b").
		task("t1", "x").changed("t1", "a").ok("t1", "b").
		task("t2", "y").skipped("t2", "a").
		add(events.Event{Type: events.PlaybookEnd, ExitCode: &code, Interrupted: true})
	sum := lastOf[Summary](t, run(Options{}, s))
	want := []HostRecap{{Host: "a", OK: 1, Changed: 1, Skipped: 1}, {Host: "b", OK: 1}}
	if sum.Verdict != VerdictInterrupted || sum.ExitCode != 130 || !reflect.DeepEqual(sum.Hosts, want) {
		t.Fatalf("summary = %+v", sum)
	}
}
