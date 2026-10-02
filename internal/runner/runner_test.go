package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgsqware/hush/internal/events"
)

// fakeAnsible writes a shell script standing in for ansible-playbook: events
// on fd 3 (as the callback would), a warning on stderr, stray stdout, exit 2.
func fakeAnsible(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ansible-playbook")
	script := `#!/bin/sh
[ -n "$ANSIBLE_CALLBACK_PLUGINS" ] && [ -f "$ANSIBLE_CALLBACK_PLUGINS/hush.py" ] || exit 99
[ "$ANSIBLE_STDOUT_CALLBACK" = hush ] && [ "$HUSH_EVENT_FD" = 3 ] || exit 98
echo '{"v":1,"ts":"2026-10-01T11:30:00.000+02:00","event":"playbook_start","playbook":{"path":"/w/site.yml","rel":"site.yml"}}' >&3
echo '[WARNING]: something odd' >&2
echo 'stray stdout'
echo '{"v":1,"ts":"2026-10-01T11:30:01.000+02:00","event":"stats","stats":{}}' >&3
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "run.jsonl")
	log, err := OpenLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var got []events.Event
	code, err := Run(context.Background(), Config{Argv: []string{fakeAnsible(t), "site.yml"}, Log: log},
		func(e events.Event) { got = append(got, e) })
	_ = log.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}

	types := map[string]int{}
	for _, e := range got {
		types[e.Type]++
	}
	if types[events.PlaybookStart] != 1 || types[events.Stats] != 1 || types[events.Stderr] != 2 {
		t.Fatalf("events = %v", types)
	}
	last := got[len(got)-1]
	if last.Type != events.PlaybookEnd || last.ExitCode == nil || *last.ExitCode != 2 {
		t.Fatalf("last event = %+v", last)
	}
	for _, e := range got {
		if e.Type == events.Stderr && e.Line == "stray stdout" && e.Source != "stdout" {
			t.Errorf("stdout line not tagged: %+v", e)
		}
	}

	// The log replays to the same events.
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	data, _ := os.ReadFile(logPath)
	if n := strings.Count(string(data), "\n"); n != len(got) {
		t.Fatalf("log has %d lines, handled %d events", n, len(got))
	}
	d := events.NewDecoder(f)
	for i := range got {
		e, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.Type != got[i].Type {
			t.Fatalf("log line %d = %s, want %s", i, e.Type, got[i].Type)
		}
	}
}

func TestRunMissingBinary(t *testing.T) {
	_, err := Run(context.Background(), Config{Argv: []string{"/nonexistent/ansible-playbook"}}, func(events.Event) {})
	if err == nil {
		t.Fatal("want an error")
	}
}

// TestInterrupt checks that cancelling sends SIGINT to the child, which can
// still report before exiting, and that playbook_end is marked interrupted.
func TestInterrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ansible-playbook")
	script := `#!/bin/sh
trap 'echo "{\"v\":1,\"ts\":\"2026-10-01T11:30:01.000+02:00\",\"event\":\"stats\",\"stats\":{}}" >&3; exit 99' INT
echo '{"v":1,"ts":"2026-10-01T11:30:00.000+02:00","event":"playbook_start","playbook":{"path":"/w/s.yml","rel":"s.yml"}}' >&3
while :; do sleep 0.05; done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var got []events.Event
	code, err := Run(ctx, Config{Argv: []string{path}}, func(e events.Event) {
		got = append(got, e)
		if e.Type == events.PlaybookStart {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 99 {
		t.Fatalf("exit code = %d, want 99 (the child's own SIGINT handling)", code)
	}
	if len(got) < 3 || got[1].Type != events.Stats {
		t.Fatalf("child could not report after SIGINT: %+v", got)
	}
	if last := got[len(got)-1]; last.Type != events.PlaybookEnd || !last.Interrupted {
		t.Fatalf("last = %+v", last)
	}
}
