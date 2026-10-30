package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jgsqware/hush/internal/pick"
)

// interactive reports whether bare `hush` should open the fuzzy finder.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// pickAndRun lets the user choose a playbook, then an inventory, and runs
// them from the playbook's Ansible project directory, so its ansible.cfg applies.
func pickAndRun(cmd *cobra.Command, g globalFlags) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, pbs := pick.Find(cwd)
	if len(pbs) == 0 {
		return fmt.Errorf("no playbook found in %s or its parent directories (run hush -- ansible-playbook <playbook>)", tilde(cwd))
	}
	color := os.Getenv("NO_COLOR") == "" && g.color != "never"
	out := cmd.OutOrStdout()
	opts := pick.Options{Color: color}

	labels := make([]string, len(pbs))
	for i, pb := range pbs {
		labels[i] = pb.Label
	}
	prompt := "playbook"
	if root != cwd {
		prompt += " in " + tilde(root)
	}
	label, err := pick.Choose(os.Stdin, out, prompt, labels, opts)
	if err != nil {
		return quietCancel(err)
	}
	var pb pick.Playbook
	for _, p := range pbs {
		if p.Label == label {
			pb = p
		}
	}

	argv := []string{"ansible-playbook", pb.Path}
	inv, err := chooseInventory(out, pb.Dir, opts)
	if err != nil {
		return quietCancel(err)
	}
	if inv != "" {
		argv = append(argv, "-i", inv)
	}
	limit, err := chooseLimit(cmd.Context(), out, pb, inv, opts)
	if err != nil {
		return quietCancel(err)
	}
	if limit != "" {
		argv = append(argv, "--limit", limit)
	}

	// Show the equivalent command, so it can be rerun or copied into a script.
	line := "$ hush -- " + strings.Join(argv, " ")
	dir := pb.Dir
	if dir == cwd {
		dir = ""
	} else {
		line = fmt.Sprintf("$ (cd %s && hush -- %s)", relOrTilde(cwd, dir), strings.Join(argv, " "))
	}
	if color {
		line = "\x1b[2m" + line + "\x1b[0m"
	}
	_, _ = fmt.Fprintln(out, line)
	return wrap(cmd, g, argv, dir)
}

// chooseInventory picks an inventory of the project in dir. The first choice
// is the project default (ansible.cfg), which adds no -i flag: it returns "".
func chooseInventory(out io.Writer, dir string, opts pick.Options) (string, error) {
	def := pick.DefaultInventory(dir)
	first := "(Ansible default inventory)"
	if def != "" {
		first = "(ansible.cfg: " + def + ")"
	}
	items := []string{first}
	for _, inv := range pick.Discover(dir).Inventories {
		if def == "" || !pick.SamePath(inv, def) {
			items = append(items, inv)
		}
	}
	if len(items) == 1 && def != "" {
		return "", nil // only the default: nothing to choose
	}
	inv, err := pick.Choose(os.Stdin, out, "inventory", items, opts)
	if err != nil || inv == first {
		return "", err
	}
	return inv, nil
}

// chooseLimit picks the hosts to run on: every host the playbook targets
// (no --limit), one of their groups, one host, or a typed --limit pattern.
// It returns "" for every targeted host.
func chooseLimit(ctx context.Context, out io.Writer, pb pick.Playbook, inv string, opts pick.Options) (string, error) {
	status := "  listing the hosts targeted by " + pb.Path + "…"
	if opts.Color {
		status = "\x1b[2m" + status + "\x1b[0m"
	}
	_, _ = fmt.Fprint(out, status)
	t, err := pick.ListTargets(ctx, pb.Dir, pb.Path, inv)
	_, _ = fmt.Fprint(out, "\r\x1b[K")
	if err != nil {
		// Not fatal: ansible-playbook will report the same problem with context.
		_, _ = fmt.Fprintf(out, "hush: cannot list hosts (%v); running on every targeted host\n", err)
		return "", nil
	}
	if len(t.Hosts) <= 1 {
		return "", nil
	}

	all := fmt.Sprintf("(all %d targeted hosts: %s)", len(t.Hosts), strings.Join(t.Patterns, ", "))
	items, values := []string{all}, map[string]string{all: ""}
	var groups []string
	for name, hosts := range t.Groups {
		if len(hosts) < len(t.Hosts) { // a group with every target is the same as all
			groups = append(groups, name)
		}
	}
	sort.Strings(groups)
	for _, name := range groups {
		label := fmt.Sprintf("@%s  (%s)", name, plural(len(t.Groups[name]), "host"))
		items, values[label] = append(items, label), name
	}
	width := 0
	for _, h := range t.Hosts {
		width = max(width, len(h))
	}
	for _, h := range t.Hosts {
		label := h
		if a := t.Address[h]; a != "" {
			label = fmt.Sprintf("%-*s  %s", width, h, a)
		}
		items, values[label] = append(items, label), h
	}

	opts.Custom = true // a typed pattern such as "azure1,azure2" or "tc_agent:&azure"
	choice, err := pick.Choose(os.Stdin, out, "hosts", items, opts)
	if err != nil {
		return "", err
	}
	if v, ok := values[choice]; ok {
		return v, nil
	}
	return choice, nil
}

// quietCancel turns Esc / Ctrl-C in the picker into a silent exit 130.
func quietCancel(err error) error {
	if errors.Is(err, pick.ErrCanceled) {
		return exitError{130}
	}
	return err
}

// relOrTilde shows dir relative to cwd when it is close, else with ~ for $HOME.
func relOrTilde(cwd, dir string) string {
	if rel, err := filepath.Rel(cwd, dir); err == nil && strings.Count(rel, "..") <= 2 {
		return rel
	}
	return tilde(dir)
}

func tilde(path string) string {
	if h, err := os.UserHomeDir(); err == nil && (path == h || strings.HasPrefix(path, h+string(filepath.Separator))) {
		return "~" + path[len(h):]
	}
	return path
}
