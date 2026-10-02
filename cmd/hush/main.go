// Command hush runs Ansible playbooks and renders calm, live output.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// A closed pipe (hush ... | head) must not kill hush mid-deployment:
	// writes fail instead, the run goes on, logs and reports are written.
	signal.Ignore(syscall.SIGPIPE)

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel() // ansible-playbook gets SIGINT; hush prints a partial summary
		<-sigs
		fmt.Fprintln(os.Stderr, "\nhush: still stopping ansible-playbook (killed after 15s)")
	}()

	err := newRootCmd().ExecuteContext(ctx)
	var exit exitError
	switch {
	case errors.As(err, &exit):
		os.Exit(exit.code) // the run's own exit code; the summary already explained it
	case err != nil:
		fmt.Fprintln(os.Stderr, "hush:", err)
		os.Exit(1)
	}
}
