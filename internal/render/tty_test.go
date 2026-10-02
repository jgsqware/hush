package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jgsqware/hush/internal/agg"
)

// statusLines returns what was drawn after each "clear line" sequence.
func statusLines(out string) []string {
	var lines []string
	for _, chunk := range strings.Split(out, clearLine)[1:] {
		if l := chunk[strings.LastIndex(chunk, "\n")+1:]; l != "" {
			lines = append(lines, ansi.Strip(l))
		}
	}
	return lines
}

func TestTTYStatusNeverWraps(t *testing.T) {
	start := time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)
	var now time.Time
	for _, width := range []int{20, 40, 80, 200} {
		now = start
		var buf bytes.Buffer
		tty := NewTTY(&buf, Style{Color: true, Glyphs: Unicode}, func() int { return width }, func() time.Time { return now })
		must(t, tty.SetSection("2/3 Monitoring stack"))
		must(t, tty.SetActivity(agg.Activity{
			Playbook: "tools/monitoring-servers/deploy-grafana.yaml",
			Task:     "Template Grafana datasources (interpolates vault secret)",
			Pending:  []string{"web-01", "mon-01", "db-01", "db-02", "cache-01"},
		}))
		now = start.Add(75 * time.Second)
		must(t, tty.Tick())

		lines := statusLines(buf.String())
		if len(lines) == 0 {
			t.Fatalf("width %d: no status line", width)
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > width-1 {
				t.Errorf("width %d: status is %d wide: %q", width, w, l)
			}
		}
		last := lines[len(lines)-1]
		if width >= 80 && (!strings.Contains(last, "1m15s") || !strings.Contains(last, "+2")) {
			t.Errorf("width %d: elapsed or pending missing: %q", width, last)
		}
	}
}

func TestTTYPermanentLinesClearStatus(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 10, 1, 11, 30, 0, 0, time.UTC)
	tty := NewTTY(&buf, Style{Glyphs: ASCII}, func() int { return 80 }, func() time.Time { return now })
	must(t, tty.SetActivity(agg.Activity{Playbook: "site.yml", Task: "x"}))
	must(t, tty.Render(agg.Notice{Text: "hello"}))
	must(t, tty.Close())

	out := buf.String()
	// status, clear, permanent line, status again, clear on close
	if !strings.Contains(out, clearLine+"   hello\n") {
		t.Fatalf("permanent line not printed on a cleared line: %q", out)
	}
	if !strings.HasSuffix(out, clearLine) {
		t.Fatalf("status line left on screen: %q", out)
	}
	if strings.Contains(out, "⠋") {
		t.Fatalf("unicode spinner with ASCII glyphs: %q", out)
	}
}
