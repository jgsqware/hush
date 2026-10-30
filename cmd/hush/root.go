package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/events"
	"github.com/jgsqware/hush/internal/runner"
)

// globalFlags are shared by every command that renders a run.
type globalFlags struct {
	color   string
	format  string
	verbose bool
	logDir  string
	junit   string
}

// exitError carries the ansible-playbook exit code up to main.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func newRootCmd() *cobra.Command {
	var g globalFlags
	root := &cobra.Command{
		Use:   "hush [flags] -- ansible-playbook <args>",
		Short: "Calm, colorful, live output for Ansible",
		Long: `Calm, colorful, live output for Ansible.

Run without arguments in a terminal to pick a playbook and an inventory
with a fuzzy finder.`,
		Example: `  hush                                   # fuzzy-find a playbook and an inventory
  hush -- ansible-playbook site.yml -i inventory --limit web
  hush replay .hush/logs/20261001-113008-site.jsonl`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if len(args) == 0 && dash < 0 && interactive() {
				return pickAndRun(cmd, g)
			}
			if dash < 0 || len(args[dash:]) == 0 {
				return cmd.Help()
			}
			return wrap(cmd, g, args[dash:], "")
		},
	}
	f := root.PersistentFlags()
	f.StringVar(&g.color, "color", "auto", "colorize output: auto, always or never (NO_COLOR is respected)")
	f.StringVar(&g.format, "format", "auto", "output format: auto, tty, plain or ci (auto: tty on a terminal, ci when GITLAB_CI or GITHUB_ACTIONS is set, else plain)")
	f.BoolVarP(&g.verbose, "verbose", "v", false, "also show tasks that were ok everywhere")
	f.StringVar(&g.logDir, "log-dir", ".hush/logs", "directory for raw event logs")
	f.StringVar(&g.junit, "junit", "", "also write a JUnit XML report to this file")

	root.AddCommand(newRunCmd(&g), newReplayCmd(&g), newVersionCmd())
	return root
}

// wrap runs a single ansible-playbook command in dir (empty: the current directory).
func wrap(cmd *cobra.Command, g globalFlags, argv []string, dir string) error {
	logPath := filepath.Join(g.logDir, time.Now().Format("20060102-150405")+"-"+slug(playbookArg(argv))+".jsonl")
	log, err := runner.OpenLog(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	out, err := newOutput(cmd, g, agg.Options{Verbose: g.verbose, Argv: argv, LogPath: logPath}, true)
	if err != nil {
		return err
	}
	if err := out.render(agg.Banner{Title: strings.Join(argv, " ")}); err != nil {
		return err
	}

	// Recorded first so a replay of the log can build rerun hints.
	cmdEvent := runner.MarshalEvent(events.Event{Type: events.Command, Argv: argv})
	if _, err := log.Write(append(cmdEvent, '\n')); err != nil {
		return err
	}
	_, runErr := runner.Run(cmd.Context(), runner.Config{Argv: argv, Dir: dir, Log: log}, out.handle)
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

// valueFlags are ansible-playbook options whose value is a separate argument.
var valueFlags = map[string]bool{
	"-i": true, "--inventory": true, "-l": true, "--limit": true, "-e": true, "--extra-vars": true,
	"-t": true, "--tags": true, "--skip-tags": true, "-u": true, "--user": true, "-f": true, "--forks": true,
	"-c": true, "--connection": true, "-T": true, "--timeout": true, "--vault-id": true,
	"--vault-password-file": true, "--private-key": true, "-M": true, "--module-path": true,
	"--start-at-task": true, "--become-user": true, "--become-method": true, "--ssh-common-args": true,
	"--ssh-extra-args": true, "--scp-extra-args": true, "--sftp-extra-args": true,
}

// playbookArg returns the first positional argument that looks like a playbook file.
func playbookArg(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if valueFlags[a] {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") && (strings.HasSuffix(a, ".yml") || strings.HasSuffix(a, ".yaml")) {
			return a
		}
	}
	return filepath.Base(argv[0])
}

var nonSlug = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// slug turns a playbook path into a file name fragment: servers/docker/deploy.yaml -> servers-docker-deploy.
func slug(path string) string {
	path = strings.TrimSuffix(strings.TrimSuffix(path, ".yaml"), ".yml")
	s := strings.Trim(nonSlug.ReplaceAllString(path, "-"), "-")
	if s == "" {
		return "run"
	}
	return s
}
