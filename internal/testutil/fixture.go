// Package testutil loads recorded runs (testdata layout) for tests.
package testutil

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgsqware/hush/internal/events"
)

// Stream returns a fixture's events with its stderr.txt lines interleaved
// as stderr events, the way hush records them live. Fixtures with a
// stderr.timeline.jsonl say where each block goes; otherwise stderr is
// placed before the last event.
func Stream(t testing.TB, dir string) []events.Event {
	t.Helper()
	evs := Decode(t, filepath.Join(dir, "events.jsonl"))

	data, err := os.ReadFile(filepath.Join(dir, "stderr.txt"))
	if errors.Is(err, os.ErrNotExist) || len(data) == 0 {
		return evs
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	stderr := func(from, n int) []events.Event {
		var out []events.Event
		for _, l := range lines[from : from+n] {
			out = append(out, events.Event{V: 1, Type: events.Stderr, Line: l})
		}
		return out
	}

	type block struct {
		Before int `json:"before_event_index"`
		First  int `json:"first_line"`
		Count  int `json:"count"`
	}
	var blocks []block
	if tl, err := os.ReadFile(filepath.Join(dir, "stderr.timeline.jsonl")); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(tl)), "\n") {
			var b block
			if err := json.Unmarshal([]byte(l), &b); err != nil {
				t.Fatal(err)
			}
			blocks = append(blocks, b)
		}
	} else {
		blocks = []block{{Before: len(evs) - 1, First: 0, Count: len(lines)}}
	}

	var out []events.Event
	for i, e := range evs {
		for _, b := range blocks {
			if b.Before == i {
				out = append(out, stderr(b.First, b.Count)...)
			}
		}
		out = append(out, e)
	}
	return out
}

// Decode reads a JSONL event file.
func Decode(t testing.TB, path string) []events.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var evs []events.Event
	d := events.NewDecoder(f)
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
