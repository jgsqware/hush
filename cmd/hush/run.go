package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/events"
	"github.com/jgsqware/hush/internal/plan"
	"github.com/jgsqware/hush/internal/runner"
)

func newRunCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "run <plan.yaml>",
		Short: "Run a multi-playbook plan with one global summary",
		Long: `Run the playbooks of a plan file, section by section, after its before
hooks and CIDR guard. The exit code is the first non-zero ansible-playbook
exit code (1 when a hook or the guard stops the plan, 130 when interrupted).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlan(cmd, *g, args[0])
		},
	}
}

func runPlan(cmd *cobra.Command, g globalFlags, path string) error {
	p, err := plan.Load(path)
	if err != nil {
		return err
	}
	logPath := filepath.Join(g.logDir, time.Now().Format("20060102-150405")+"-"+slug(filepath.Base(path))+".jsonl")
	log, err := runner.OpenLog(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	out, err := newOutput(cmd, g, agg.Options{Verbose: g.verbose, LogPath: logPath}, true)
	if err != nil {
		return err
	}
	r := &planRun{ctx: cmd.Context(), plan: p, out: out, log: log, exec: systemExec{}, now: time.Now}
	if err := out.render(agg.Banner{
		Title:  filepath.Base(path),
		Detail: fmt.Sprintf("%s · %s", plural(len(p.Sections), "section"), plural(p.Count(), "playbook")),
	}); err != nil {
		return err
	}

	runErr := r.run()
	sum, err := out.finish()
	if runErr != nil {
		return runErr
	}
	if err != nil {
		return err
	}
	if sum.ExitCode != 0 {
		return exitError{sum.ExitCode}
	}
	return nil
}

type planRun struct {
	ctx    context.Context
	plan   *plan.Plan
	out    *output
	log    io.Writer
	exec   executor
	now    func() time.Time // timestamps of the events hush writes
	logErr error
}

// executor runs the parts of a plan; tests replace it with recorded runs.
type executor interface {
	hook(ctx context.Context, h plan.Hook, dir string, env []string) events.HookResult
	guard(ctx context.Context, g *plan.Guard, dir string, env []string) events.GuardResult
	playbook(ctx context.Context, pb plan.Playbook, cfg runner.Config, handle func(events.Event)) (int, error)
}

type systemExec struct{}

func (systemExec) hook(ctx context.Context, h plan.Hook, dir string, env []string) events.HookResult {
	return plan.RunHook(ctx, h, dir, env)
}

func (systemExec) guard(ctx context.Context, g *plan.Guard, dir string, env []string) events.GuardResult {
	return plan.CheckGuard(ctx, g, dir, env, plan.SystemResolver)
}

func (systemExec) playbook(ctx context.Context, _ plan.Playbook, cfg runner.Config, handle func(events.Event)) (int, error) {
	return runner.Run(ctx, cfg, handle)
}

// emit records an event written by hush and renders it.
func (r *planRun) emit(e events.Event) {
	e.TS = r.now()
	b := runner.MarshalEvent(e)
	if r.logErr == nil {
		_, r.logErr = r.log.Write(append(b, '\n'))
	}
	r.out.handle(events.Parse(b))
}

func (r *planRun) run() error {
	p, env := r.plan, r.plan.Environ()

	for _, h := range p.Before {
		r.out.setSection("before: " + h.Name)
		res := r.exec.hook(r.ctx, h, p.Dir, env)
		r.emit(events.Event{Type: events.Hook, HookResult: &res})
		if res.ExitCode != 0 {
			r.skip(0, 0, "before hook failed")
			return r.logErr
		}
	}
	if p.Guard != nil {
		r.out.setSection("guard")
		res := r.exec.guard(r.ctx, p.Guard, p.Dir, env)
		r.emit(events.Event{Type: events.Guard, GuardResult: &res})
		if !res.OK {
			r.skip(0, 0, "guard refused")
			return r.logErr
		}
	}

	failed := false
	for si, s := range p.Sections {
		r.emit(events.Event{Type: events.Section, SectionInfo: &events.SectionInfo{Index: si + 1, Count: len(p.Sections), Name: s.Name}})
		r.out.setSection(fmt.Sprintf("%d/%d %s", si+1, len(p.Sections), s.Name))
		for pi, pb := range s.Playbooks {
			switch {
			case r.ctx.Err() != nil:
				r.skip(si, pi, "interrupted")
				return r.logErr
			case failed && p.FailFast:
				r.skip(si, pi, "fail_fast")
				return r.logErr
			}
			argv := pb.Argv()
			r.emit(events.Event{Type: events.Command, Argv: argv})
			code, err := r.exec.playbook(r.ctx, pb, runner.Config{Argv: argv, Env: env, Dir: p.Dir, Log: r.log}, r.out.handle)
			if err != nil {
				return err
			}
			failed = failed || code != 0
		}
	}
	return r.logErr
}

// skip reports the playbooks from section si, playbook pi onwards as not run.
func (r *planRun) skip(si, pi int, reason string) {
	var paths []string
	for i := si; i < len(r.plan.Sections); i++ {
		pbs := r.plan.Sections[i].Playbooks
		if i == si {
			pbs = pbs[pi:]
		}
		for _, pb := range pbs {
			paths = append(paths, pb.Path)
		}
	}
	if len(paths) > 0 {
		r.emit(events.Event{Type: events.NotRun, Skipped: paths, Reason: reason})
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
