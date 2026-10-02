package events

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var known = map[string]bool{
	PlaybookStart: true, PlayStart: true, TaskStart: true, RunnerStart: true,
	RunnerOK: true, RunnerFailed: true, RunnerSkipped: true, RunnerUnreachable: true,
	RunnerRetry: true, ItemOK: true, ItemFailed: true, ItemSkipped: true,
	Include: true, HandlerNotified: true, NoHostsMatched: true, NoHostsRemaining: true,
	Warning: true, Deprecation: true, Stats: true, PlaybookEnd: true, Stderr: true,
	Command: true, Hook: true, Guard: true, Section: true, NotRun: true,
}

// TestFixturesDecode checks every recorded stream decodes into known, well-formed events.
func TestFixturesDecode(t *testing.T) {
	files, err := filepath.Glob("../../testdata/*/*/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, f := range files {
		t.Run(strings.TrimPrefix(f, "../../testdata/"), func(t *testing.T) {
			evs := decodeFile(t, f)
			if len(evs) == 0 {
				t.Fatal("empty stream")
			}
			if evs[0].Type != PlaybookStart {
				t.Errorf("first event = %q, want %q", evs[0].Type, PlaybookStart)
			}
			for i, e := range evs {
				if !known[e.Type] {
					t.Errorf("event %d: unknown type %q (line %q)", i, e.Type, e.Line)
				}
				if e.V != SchemaVersion {
					t.Errorf("event %d: v = %d", i, e.V)
				}
				if e.TS.IsZero() {
					t.Errorf("event %d: missing ts", i)
				}
				switch e.Type {
				case TaskStart:
					if e.Task == nil || e.Task.UUID == "" || e.Task.Name == "" {
						t.Errorf("event %d: incomplete task_start", i)
					}
				case RunnerOK, RunnerFailed, RunnerSkipped, RunnerUnreachable:
					if e.Host == "" || e.TaskUUID == "" || e.Result == nil {
						t.Errorf("event %d: incomplete %s", i, e.Type)
					}
				case ItemOK, ItemFailed, ItemSkipped:
					if e.Item == nil {
						t.Errorf("event %d: %s without item", i, e.Type)
					}
				}
			}
		})
	}
}

func TestTolerant(t *testing.T) {
	in := `{"v":1,"ts":"2026-10-01T11:30:08.000+02:00","event":"task_start","task":{"uuid":"u1","name":"x","future":1},"brand_new":true}

not json
{"v":2,"ts":"2026-10-01T11:30:09.000+02:00","event":"from_the_future"}
{"no_event_field":1}
{"v":1,"ts":"2026-10-01T11:30:10.000+02:00","event":"runner_skipped","host":"h","result":{"false_condition":false,"skip_reason":"x"}}
`
	d := NewDecoder(strings.NewReader(in))
	var types []string
	for {
		e, err := d.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, e.Type)
	}
	want := []string{TaskStart, Invalid, "from_the_future", Invalid, RunnerSkipped}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v, want %v", types, want)
	}
}

func TestMsgText(t *testing.T) {
	cases := map[string]string{
		`"Task failed: Failed to connect"`: "Failed to connect",
		`["a","b"]`:                        `["a","b"]`,
		`null`:                             "",
	}
	for raw, want := range cases {
		r := Result{Msg: []byte(raw)}
		if got := r.MsgText(); got != want {
			t.Errorf("MsgText(%s) = %q, want %q", raw, got, want)
		}
	}
}

func decodeFile(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var evs []Event
	d := NewDecoder(f)
	for {
		e, err := d.Next()
		if errors.Is(err, io.EOF) {
			return evs
		}
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
}
