package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jgsqware/hush/internal/agg"
	"github.com/jgsqware/hush/internal/events"
)

func newReplayCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "replay <run.jsonl>",
		Short: "Re-render a recorded run",
		Long:  "Re-render a run recorded in --log-dir. Exits with the recorded exit code.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			out, err := newOutput(cmd, *g, agg.Options{Verbose: g.verbose, LogPath: args[0]}, false)
			if err != nil {
				return err
			}
			if err := out.render(agg.Banner{Title: filepath.Base(args[0]), Detail: "replay"}); err != nil {
				return err
			}
			d := events.NewDecoder(f)
			for {
				e, err := d.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				out.handle(e)
			}
			sum, err := out.finish()
			if err != nil {
				return err
			}
			if sum.ExitCode != 0 {
				return exitError{sum.ExitCode}
			}
			return nil
		},
	}
}
