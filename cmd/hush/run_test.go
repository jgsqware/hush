package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/events"
	"github.com/jgsqware/hush/internal/plan"
	"github.com/jgsqware/hush/internal/render"
	"github.com/jgsqware/hush/internal/runner"
	"github.com/jgsqware/hush/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite .golden files")

// fixtureExec replays the recorded lab run described by testdata/plan.yaml.
// clock follows the recorded timestamps.
type fixtureExec struct {
	t     *testing.T
	clock *time.Time
}

func (f fixtureExec) hook(_ context.Context, h plan.Hook, _ string, _ []string) events.HookResult {
	out, err := os.ReadFile("../../testdata/hooks/" + h.Name + ".out")
	if err != nil {
		f.t.Fatal(err)
	}
	res := plan.Collapse(h, out)
	res.Tail, res.DurationMS = nil, 400
	return res
}

func (f fixtureExec) guard(ctx context.Context, g *plan.Guard, _ string, _ []string) events.GuardResult {
	inv, err := os.ReadFile("../../testdata/inventory.list.json")
	if err != nil {
		f.t.Fatal(err)
	}
	return plan.Check(ctx, g, inv, plan.SystemResolver)
}

func (f fixtureExec) playbook(_ context.Context, pb plan.Playbook, cfg runner.Config, handle func(events.Event)) (int, error) {
	code := 0
	for _, e := range testutil.Stream(f.t, filepath.Join("../../testdata", pb.Fixture)) {
		if _, err := cfg.Log.Write(append(runner.MarshalEvent(e), '\n')); err != nil {
			return 0, err
		}
		handle(e)
		if !e.TS.IsZero() {
			*f.clock = e.TS
		}
		if e.Type == events.PlaybookEnd && e.ExitCode != nil {
			code = *e.ExitCode
		}
	}
	return code, nil
}

// replayPlan runs testdata/plan.yaml against the recorded runs.
func replayPlan(t *testing.T, r render.Renderer, failFast bool, variant map[string]string) (agg.Summary, []byte) {
	t.Helper()
	p, err := plan.Load("../../testdata/plan.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p.FailFast = failFast
	for si := range p.Sections {
		for pi, pb := range p.Sections[si].Playbooks {
			if v, ok := variant[pb.Fixture]; ok {
				p.Sections[si].Playbooks[pi].Fixture = v
			}
		}
	}
	var log bytes.Buffer
	out := &output{agg: agg.New(agg.Options{LogPath: ".hush/logs/plan.jsonl"}), r: r}
	// hush's own events take the time of the recorded run, as they would live.
	clock := time.Date(2026, 10, 1, 11, 30, 8, 0, time.FixedZone("CEST", 2*3600))
	exec := fixtureExec{t: t, clock: &clock}
	run := &planRun{ctx: context.Background(), plan: p, out: out, log: &log, exec: exec, now: func() time.Time { return clock }}
	if err := out.render(agg.Banner{Title: "plan.yaml", Detail: "3 sections · 8 playbooks"}); err != nil {
		t.Fatal(err)
	}
	if err := run.run(); err != nil {
		t.Fatal(err)
	}
	sum, err := out.finish()
	if err != nil {
		t.Fatal(err)
	}
	return sum, log.Bytes()
}

type expectedPlan struct {
	Verdict      string                         `yaml:"verdict"`
	ExitCode     int                            `yaml:"exit_code"`
	Playbooks    int                            `yaml:"playbooks"`
	Hosts        []string                       `yaml:"hosts"`
	DurationS    int                            `yaml:"duration_s"`
	TotalChanged int                            `yaml:"total_changed"`
	Recap        map[string]map[string]int      `yaml:"recap"`
	BeforeHooks  map[string]map[string][]string `yaml:"before_hooks"`
}

// TestPlanMatchesExpectedSummary replays the 8-playbook lab run through the
// real plan orchestration and checks the global summary.
func TestPlanMatchesExpectedSummary(t *testing.T) {
	var buf bytes.Buffer
	sum, _ := replayPlan(t, render.NewPlain(&buf, render.Style{Glyphs: render.Unicode}, 100), true, nil)

	data, err := os.ReadFile("../../testdata/expected-plan-summary.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var want expectedPlan
	if err := yaml.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if sum.Verdict != agg.VerdictSuccess || sum.ExitCode != want.ExitCode || sum.Playbooks != want.Playbooks {
		t.Errorf("verdict/exit/playbooks = %v/%d/%d", sum.Verdict, sum.ExitCode, sum.Playbooks)
	}
	if sum.Changed != want.TotalChanged {
		t.Errorf("changed = %d, want %d", sum.Changed, want.TotalChanged)
	}
	if got := sum.Duration.Round(time.Second); got != time.Duration(want.DurationS)*time.Second {
		t.Errorf("duration = %v, want %ds", got, want.DurationS)
	}
	var hosts []string
	for _, h := range sum.Hosts {
		hosts = append(hosts, h.Host)
		w := want.Recap[h.Host]
		got := map[string]int{"ok": h.OK, "changed": h.Changed, "failures": h.Failed, "unreachable": h.Unreachable,
			"skipped": h.Skipped, "rescued": h.Rescued, "ignored": h.Ignored}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("recap %s = %v, want %v", h.Host, got, w)
		}
	}
	sort.Strings(hosts)
	if !reflect.DeepEqual(hosts, want.Hosts) {
		t.Errorf("hosts = %v, want %v", hosts, want.Hosts)
	}
}

func TestPlanFailFast(t *testing.T) {
	var buf bytes.Buffer
	sum, _ := replayPlan(t, render.NewPlain(&buf, render.Style{Glyphs: render.Unicode}, 100), true,
		map[string]string{"runs/02-caddy-deploy": "variants/caddy-failed"})
	if sum.Verdict != agg.VerdictFailed || sum.ExitCode != 2 || sum.Playbooks != 2 || sum.NotRun != 6 {
		t.Fatalf("summary = %+v", sum)
	}
	sum, _ = replayPlan(t, render.NewPlain(&buf, render.Style{Glyphs: render.Unicode}, 100), false,
		map[string]string{"runs/02-caddy-deploy": "variants/caddy-failed"})
	if sum.ExitCode != 2 || sum.Playbooks != 8 || sum.NotRun != 0 {
		t.Fatalf("without fail_fast: %+v", sum)
	}
}

// TestPlanGolden renders the whole plan, and checks that replaying the plan
// log gives the same output as the live run.
func TestPlanGolden(t *testing.T) {
	for _, m := range []struct {
		file string
		make func(*bytes.Buffer) render.Renderer
	}{
		{"plan.plain.golden", func(b *bytes.Buffer) render.Renderer {
			return render.NewPlain(b, render.Style{Glyphs: render.Unicode}, 100)
		}},
		{"plan.ci-github.golden", func(b *bytes.Buffer) render.Renderer {
			return render.NewCI(b, render.Style{Glyphs: render.Unicode}, 100, render.GitHub, time.Now)
		}},
	} {
		t.Run(m.file, func(t *testing.T) {
			var live bytes.Buffer
			_, log := replayPlan(t, m.make(&live), true, nil)
			golden := filepath.Join("../../testdata", m.file)
			if *update {
				if err := os.WriteFile(golden, live.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if want, err := os.ReadFile(golden); err != nil || !bytes.Equal(live.Bytes(), want) {
				t.Errorf("differs from %s (run with -update):\n%s", golden, live.Bytes())
			}

			// Replay of the log: same items as the live run, after the banner.
			var replayed bytes.Buffer
			r := m.make(&replayed)
			a := agg.New(agg.Options{LogPath: ".hush/logs/plan.jsonl"})
			_ = r.Render(agg.Banner{Title: "plan.yaml", Detail: "3 sections · 8 playbooks"})
			d := events.NewDecoder(bytes.NewReader(log))
			for {
				e, err := d.Next()
				if err != nil {
					break
				}
				_ = r.Render(a.Handle(e)...)
			}
			_ = r.Render(a.Finish()...)
			_ = r.Close()
			if !bytes.Equal(replayed.Bytes(), live.Bytes()) {
				t.Errorf("replay of the plan log differs from the live output:\n%s", replayed.Bytes())
			}
		})
	}
}
