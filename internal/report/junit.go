// Package report writes machine-readable reports of a run.
package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jgsqware/hush/internal/events"
)

// JUnit builds a JUnit XML report from the event stream: one test suite per
// playbook, one test case per task and host.
type JUnit struct {
	suites []*suite
	cur    *suite
	base   string // playbook dir, stripped from task paths
	play   string
	tasks  map[string]taskInfo
}

type taskInfo struct {
	name, play, file string
	line             int
}

// NewJUnit returns an empty report.
func NewJUnit() *JUnit { return &JUnit{tasks: map[string]taskInfo{}} }

type testsuites struct {
	XMLName  xml.Name `xml:"testsuites"`
	Name     string   `xml:"name,attr"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Time     float64  `xml:"time,attr"`
	Suites   []*suite `xml:"testsuite"`
}

type suite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      float64     `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr,omitempty"`
	Cases     []*testcase `xml:"testcase"`

	start, last time.Time
	exitCode    int
}

type testcase struct {
	Name      string   `xml:"name,attr"`
	Classname string   `xml:"classname,attr"`
	File      string   `xml:"file,attr,omitempty"`
	Line      int      `xml:"line,attr,omitempty"`
	Time      float64  `xml:"time,attr"`
	Failure   *problem `xml:"failure,omitempty"`
	Error     *problem `xml:"error,omitempty"`
	Skipped   *skipped `xml:"skipped,omitempty"`
	SystemOut string   `xml:"system-out,omitempty"`
}

type problem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type skipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// Handle consumes one event.
func (j *JUnit) Handle(e events.Event) {
	switch e.Type {
	case events.PlaybookStart:
		j.cur = &suite{start: e.TS}
		if e.Playbook != nil {
			j.cur.Name = e.Playbook.Rel
			j.base = strings.TrimSuffix(e.Playbook.Path, e.Playbook.Rel)
		}
		if !e.TS.IsZero() {
			j.cur.Timestamp = e.TS.UTC().Format("2006-01-02T15:04:05")
		}
		j.suites = append(j.suites, j.cur)
	case events.PlayStart:
		if e.Play != nil {
			j.play = e.Play.Name
		}
	case events.TaskStart:
		if e.Task != nil {
			j.tasks[e.Task.UUID] = taskInfo{
				name: e.Task.Name, play: j.play,
				file: strings.TrimPrefix(e.Task.Path, j.base), line: e.Task.Line,
			}
		}
	case events.RunnerOK, events.RunnerFailed, events.RunnerSkipped, events.RunnerUnreachable:
		j.result(e)
	case events.PlaybookEnd:
		s := j.cur
		if s == nil {
			// ansible-playbook failed before starting a playbook.
			s = &suite{Name: "ansible-playbook"}
			j.suites = append(j.suites, s)
		}
		if e.ExitCode != nil {
			s.exitCode = *e.ExitCode
		}
		if s.exitCode != 0 && s.Failures+s.Errors == 0 {
			s.Cases = append(s.Cases, &testcase{
				Name: "ansible-playbook", Classname: s.Name,
				Error: &problem{Type: "exit", Message: fmt.Sprintf("ansible-playbook exited with code %d", s.exitCode)},
			})
			s.Errors++
			s.Tests++
		}
		j.cur = nil
	}
	if j.cur != nil && !e.TS.IsZero() {
		j.cur.last = e.TS
	}
}

func (j *JUnit) result(e events.Event) {
	if j.cur == nil || e.Result == nil {
		return
	}
	info := j.tasks[e.TaskUUID]
	c := &testcase{
		Name:      fmt.Sprintf("%s [%s]", info.name, e.Host),
		Classname: info.play,
		File:      info.file,
		Line:      info.line,
		Time:      float64(e.DurationMS) / 1000,
	}
	r := e.Result
	msg := r.MsgText()
	switch e.Type {
	case events.RunnerOK:
		if r.Changed {
			c.SystemOut = "changed"
		}
	case events.RunnerSkipped:
		c.Skipped = &skipped{Message: r.SkipReason}
		j.cur.Skipped++
	case events.RunnerUnreachable:
		c.Error = &problem{Type: "unreachable", Message: msg}
		j.cur.Errors++
	case events.RunnerFailed:
		switch {
		case e.IgnoreErrors:
			c.SystemOut = "failed (ignored): " + msg
		case r.Rescuable:
			c.SystemOut = "failed (rescued): " + msg
		default:
			c.Failure = &problem{Type: "failed", Message: msg, Text: details(r)}
			j.cur.Failures++
		}
	}
	j.cur.Cases = append(j.cur.Cases, c)
	j.cur.Tests++
	j.cur.Time += c.Time
}

func details(r *events.Result) string {
	var b strings.Builder
	if r.RC != nil {
		fmt.Fprintf(&b, "rc: %d\n", *r.RC)
	}
	if r.Stdout != "" {
		fmt.Fprintf(&b, "stdout:\n%s\n", r.Stdout)
	}
	if r.Stderr != "" {
		fmt.Fprintf(&b, "stderr:\n%s\n", r.Stderr)
	}
	return b.String()
}

// Write encodes the report.
func (j *JUnit) Write(w io.Writer) error {
	all := testsuites{Name: "hush", Suites: j.suites}
	for _, s := range j.suites {
		if !s.start.IsZero() && s.last.After(s.start) {
			s.Time = s.last.Sub(s.start).Seconds() // wall time beats the sum of task times
		}
		all.Tests += s.Tests
		all.Failures += s.Failures
		all.Errors += s.Errors
		all.Skipped += s.Skipped
		all.Time += s.Time
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(all); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// WriteFile writes the report to path.
func (j *JUnit) WriteFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := j.Write(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
