# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `hush` with no arguments in a terminal opens a built-in fuzzy finder to pick a playbook,
  then an inventory (or the ansible.cfg default), and runs them. It searches the enclosing
  Ansible project, or parent directories when the current one has no playbook, and runs
  each playbook from its project directory. A last step narrows the run with `--limit`: one of
  the targeted hosts, one of their groups, or a typed pattern.

## [0.1.0] - 2026-10-02

First release.

### Added

- `hush -- ansible-playbook …` wraps a single run; `hush run plan.yaml` runs a
  multi-playbook plan with one global summary; `hush replay` re-renders a recorded run;
  `hush version`.
- Embedded Ansible stdout callback emitting a versioned JSON event stream on a dedicated
  file descriptor. Tested on ansible-core 2.16, 2.20 and 2.21.
- Aggregation: ok and skipped tasks are only counted; changed tasks are grouped per task
  across hosts; failures and unreachable hosts are shown immediately with msg,
  stdout/stderr, `file:line` and a `--start-at-task` rerun command; loops as `x/y items`;
  debug output as info boxes; ignored and rescued failures; linear and free strategies.
- Warnings and deprecations from stderr and from the callback are deduplicated, counted with
  their hosts and files, and shown once with a known fix. The fix table is embedded and can
  be extended in `~/.config/hush/fixes.yaml`.
- Output modes: `tty` with a live status line, `plain`, `ci` with GitLab and GitHub
  collapsible sections; semantic colors with `NO_COLOR` support; ASCII fallback.
- `--junit` report.
- Plans: before hooks collapsed to one line, CIDR guard over the inventory, `env`,
  sections, `fail_fast`.
- Raw event logs in `.hush/logs/`, replayable to the same output.
- Ctrl-C is forwarded to `ansible-playbook`; hush waits and prints a partial summary marked
  `INTERRUPTED` (exit 130). A closed stdout no longer stops a deployment.

[0.1.0]: https://github.com/jgsqware/hush/releases/tag/v0.1.0
