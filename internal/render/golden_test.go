package render

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite .golden files")

// Terminal widths used by golden files: the tty one is narrower on purpose.
const (
	goldenWidth    = 100
	goldenTTYWidth = 80
)

// mode builds a renderer for one golden file; clock is the fake time,
// advanced to each event's timestamp.
type mode struct {
	file string
	make func(w io.Writer, clock func() time.Time) Renderer
}

var modes = []mode{
	{"plain.golden", func(w io.Writer, _ func() time.Time) Renderer {
		return NewPlain(w, Style{Glyphs: Unicode}, goldenWidth)
	}},
	{"ci-gitlab.golden", func(w io.Writer, clock func() time.Time) Renderer {
		return NewCI(w, Style{Color: true, Glyphs: Unicode}, goldenWidth, GitLab, clock)
	}},
	{"ci-github.golden", func(w io.Writer, clock func() time.Time) Renderer {
		return NewCI(w, Style{Glyphs: Unicode}, goldenWidth, GitHub, clock)
	}},
	{"tty.golden", func(w io.Writer, clock func() time.Time) Renderer {
		return NewTTY(w, Style{Color: true, Glyphs: Unicode}, func() int { return goldenTTYWidth }, clock)
	}},
}

// TestGolden replays every fixture through each render mode and compares the
// output with the .golden file next to it.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob("../../testdata/*/*/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range dirs {
		dir := filepath.Dir(f)
		name := strings.TrimPrefix(dir, "../../testdata/")
		for _, m := range modes {
			t.Run(name+"/"+m.file, func(t *testing.T) {
				var buf bytes.Buffer
				var now time.Time
				r := m.make(&buf, func() time.Time { return now })
				replay(t, f, r, func(ts time.Time) { now = ts })
				compareGolden(t, filepath.Join(dir, m.file), buf.Bytes())
			})
		}
	}
}

// TestColorAndASCII pins the escape codes and the ASCII fallback on one fixture.
func TestColorAndASCII(t *testing.T) {
	const dir = "../../testdata/lab/2.21-fail"
	var buf bytes.Buffer
	replay(t, dir+"/events.jsonl", NewPlain(&buf, Style{Color: true, Glyphs: ASCII}, goldenWidth), nil)
	compareGolden(t, dir+"/plain-color-ascii.golden", buf.Bytes())
}

// replay renders a fixture as a live run would: items, then (for live
// renderers) the activity and one spinner tick per event. tick, if set, is
// told each event's timestamp before it is handled.
func replay(t *testing.T, path string, r Renderer, tick func(time.Time)) {
	t.Helper()
	a := agg.New(agg.Options{LogPath: ".hush/logs/replay.jsonl"})
	must(t, r.Render(agg.Banner{Title: "replay"}))
	live, _ := r.(Live)
	for _, e := range testutil.Stream(t, filepath.Dir(path)) {
		if tick != nil && !e.TS.IsZero() {
			tick(e.TS)
		}
		must(t, r.Render(a.Handle(e)...))
		if live != nil {
			must(t, live.SetActivity(a.Activity()))
			must(t, live.Tick())
		}
	}
	must(t, r.Render(a.Finish()...))
	must(t, r.Close())
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		must(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s (run with -update to accept)\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
