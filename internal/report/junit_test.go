package report

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"testing"

	"github.com/jgsqware/hush/internal/events"
)

var update = flag.Bool("update", false, "rewrite .golden files")

func TestJUnitGolden(t *testing.T) {
	for _, dir := range []string{
		"../../testdata/lab/2.21-site",
		"../../testdata/lab/2.21-fail",
		"../../testdata/variants/caddy-failed",
	} {
		t.Run(dir, func(t *testing.T) {
			f, err := os.Open(dir + "/events.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			j := NewJUnit()
			d := events.NewDecoder(f)
			for {
				e, err := d.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				j.Handle(e)
			}
			var buf bytes.Buffer
			if err := j.Write(&buf); err != nil {
				t.Fatal(err)
			}
			golden := dir + "/junit.golden.xml"
			if *update {
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("differs from %s:\n%s", golden, buf.Bytes())
			}
		})
	}
}

func TestJUnitPlaybookNeverStarted(t *testing.T) {
	code := 1
	j := NewJUnit()
	j.Handle(events.Event{Type: events.PlaybookEnd, ExitCode: &code})
	var buf bytes.Buffer
	if err := j.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`errors="1"`)) || !bytes.Contains(buf.Bytes(), []byte("exited with code 1")) {
		t.Fatalf("got %s", buf.Bytes())
	}
}
