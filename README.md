# hush

**Calm, colorful, live output for Ansible.**

Ansible's default output buries the three lines that matter under hundreds of `ok:`.
hush runs `ansible-playbook` for you and shows only what changed, what failed and what
needs fixing, then ends with a clear verdict. Nothing is lost: the full event stream is
saved and can be replayed.

![hush running a plan](docs/demo.gif)

```text
◆ hush  plan.yaml  3 sections · 8 playbooks

 ✓ before devault  1125 · 302                                              <1s
 ✓ guard  2 hosts in 192.0.2.0/24

━━ 1/3  Docker hosts + Caddy ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 ▸ tools/caddy/deploy.yaml   web-01 mon-01
   ● Create an container with caddy-custom     web-01 mon-01
   ● Restart caddy                             web-01 mon-01
   ✓ 4 ok  ● 2 changed                                                       3s

━━ 2/3  Monitoring stack (mon-01) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 ▸ tools/monitoring-servers/deploy-loki.yaml   mon-01   ✓ 6 ok  ⊘ 1 skip   unchanged   2s
 ▸ tools/monitoring-servers/deploy-grafana.yaml   mon-01
   ● Ensure Grafana config directories exist   3/4 items  …/provisioning, …/dashboards
   ✓ 2 ok  ● 6 changed                                                       5s

━━ Summary ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 ✔ SUCCESS  8 playbooks · 2 hosts · 0 failed · 15 changed · 57s

   host    ok   chg  skip  fail  unreach
   web-01  41   5    13    0     0
   mon-01  59   10   11    0     0

 ⚠ To fix (deduplicated)
   ssh   sftp/scp failed → fallback ×20 · 2 hosts  → ansible_ssh_transfer_method=piped
   2.24  INJECT_FACTS_AS_VARS ×9 · 6 files         → ansible_facts['user_dir'] (no ansible_ prefix)

 logs  .hush/logs/20261001-113008-plan.jsonl
```

## Install

```sh
brew install --cask jgsqware/tap/hush                   # macOS
go install github.com/jgsqware/hush/cmd/hush@latest     # from source
docker run --rm -v "$PWD:/work" ghcr.io/jgsqware/hush -- ansible-playbook site.yml   # ansible-core included
```

Or download a binary from the [releases](https://github.com/jgsqware/hush/releases).
hush is a single static binary; it only needs `ansible-playbook` (ansible-core 2.16 or later).

## Quickstart

Put `hush --` in front of the command you already run:

```sh
hush -- ansible-playbook site.yml -i inventory --limit web
```

Or just type `hush`: a fuzzy finder lists the playbooks, then the inventories of the
chosen playbook's project (its `ansible.cfg` default first). From a subdirectory, hush
searches the whole Ansible project (the nearest `ansible.cfg` above); from a directory
without playbooks, it looks one or two levels up, so a sibling Ansible repo is found.
Each playbook runs from its project directory, so its `ansible.cfg` applies.
A last step picks the hosts: all the hosts the playbook targets, one of their groups
(`@azure`), a single host (`azure24`, searchable by name or address), or any `--limit`
pattern you type (`azure1,azure2`, `tc_agent:&on_prem`). hush asks Ansible itself
(`ansible-playbook --list-hosts`), so constructed groups and host patterns are resolved
exactly. Type to filter, `↑`/`↓` to move, `Enter` to
pick, `Esc` to cancel. hush prints the equivalent `hush -- ansible-playbook …` command and
runs it.

Run several playbooks with one global summary, after optional checks:

```sh
hush run plan.yaml
```

Re-render a recorded run (every run is recorded in `.hush/logs/`):

```sh
hush replay .hush/logs/20261001-113008-site.jsonl
```

The exit code is the one of `ansible-playbook` (for plans: the first non-zero one).

## What you see

| Ansible prints | hush prints |
|---|---|
| `ok:` / `skipping:` for every task and host | nothing; counted in the playbook's `✓ 22 ok  ⊘ 8 skip` line |
| `changed: [web-01]`, `changed: [mon-01]` | `● Restart caddy   web-01 mon-01`, one line per task; diverging hosts show their status |
| one `changed:` line per loop item | `3/4 items` and the first labels |
| `fatal: … FAILED! => {json}` | an immediate block: msg, rc, stdout/stderr (truncated with `+N lines`), `file:line`, and a ready-to-copy `--start-at-task` rerun command |
| `UNREACHABLE!` | the same block, right away |
| `...ignoring` / a rescued failure | `~ … ignored: msg` / `↺ … rescued: msg` |
| `debug: msg=` output | an info box |
| the same `[WARNING]` again and again | deduplicated at the end, counted, with the affected hosts or files and a known fix |
| `PLAY RECAP` | a verdict line and a per-host table |

On a terminal, a status line at the bottom shows the current playbook, task, pending hosts
and elapsed time. It is redrawn in place and never wraps; hush never reads the keyboard.

## Flags

| Flag | Default | |
|---|---|---|
| `--format` | `auto` | `tty` (live status line), `plain` (append-only), `ci` (plain + collapsible sections). `auto` picks `tty` on a terminal, `ci` when `GITLAB_CI` or `GITHUB_ACTIONS` is set, else `plain`. |
| `--color` | `auto` | `always`, `never`. `auto` colors terminals and CI logs; `NO_COLOR` turns colors off. |
| `-v`, `--verbose` | off | also show tasks that were ok everywhere |
| `--log-dir` | `.hush/logs` | where raw event logs go |
| `--junit <file>` | | also write a JUnit XML report (one suite per playbook, one case per task and host) |

Glyphs fall back to ASCII when the locale is not UTF-8.

## Plans

```yaml
before:                         # optional; each hook's output collapses into one line
  - name: devault
    run: ./scripts/lab-devault.sh
    summary_regex: '(\d+) bloc.* (\d+) fichier'
guard:                          # optional; refuse to run outside these networks
  allowed_cidrs: [192.0.2.0/24]
  inventory: inventories/lab.yaml
env: { ANSIBLE_CONFIG: ansible.cfg }
sections:
  - name: Docker hosts + Caddy
    playbooks:
      - { path: servers/docker/deploy_full.yaml, limit: "web-01,mon-01" }
      - { path: tools/caddy/deploy.yaml, limit: "web-01,mon-01", args: ["--diff"] }
fail_fast: true
```

See the [plan reference](docs/plan.md) for every key.

## In CI

hush detects GitLab CI and GitHub Actions and switches to `ci` output: one collapsible
section per playbook, failures and recaps always outside sections so a collapsed section
never hides them. Add `--junit report.xml` to feed the test report of your CI.

```yaml
# .gitlab-ci.yml
deploy:
  image: ghcr.io/jgsqware/hush:latest
  script:
    - hush --junit report.xml run plan.yaml
  artifacts:
    when: always
    reports: { junit: report.xml }
    paths: [.hush/logs/]
```

## Known fixes

Warnings are matched against a table of known fixes embedded in hush
([`internal/warnings/fixes.yaml`](internal/warnings/fixes.yaml)). Add your own rules in
`~/.config/hush/fixes.yaml` (or `$XDG_CONFIG_HOME/hush/fixes.yaml`); they are checked first,
so they can also override a built-in rule:

```yaml
rules:
  - id: ssh_transfer_fallback
    match: 'transfer mechanism failed'
    tag: ssh
    label: sftp/scp failed → fallback
    fix: set ansible_ssh_transfer_method=piped in group_vars/all.yml
```

## How it works

```text
ansible-playbook ──fd 3──▶ events ──▶ aggregator ──▶ renderer (tty / plain / ci)
  (hush callback)           │            │
        stderr ─────────────┤            └──▶ JUnit
                            └──▶ .hush/logs/<time>-<name>.jsonl
```

- hush ships its own Ansible stdout callback (Python, embedded in the binary). It is
  written to a temporary directory and enabled with `ANSIBLE_CALLBACK_PLUGINS` and
  `ANSIBLE_STDOUT_CALLBACK=hush`. It writes one JSON event per line on a dedicated file
  descriptor, so stray output on stdout cannot corrupt the stream. The schema is versioned
  ([`testdata/README.md`](testdata/README.md)).
- stderr is read too: `[WARNING]` and `[DEPRECATION WARNING]` blocks (with their
  `Origin: file:line:col` context) are parsed and deduplicated with the warnings that
  ansible-core 2.19+ reports through the callback.
- The aggregator groups results per task across hosts and decides what is worth showing.
  It is pure (no I/O, no clock); renderers only format its output.
- Everything is recorded, including hush's own events (hooks, guard, sections): replaying a
  log prints exactly what the live run printed.
- On Ctrl-C, hush forwards the signal to `ansible-playbook` (which runs in its own process
  group), waits for it, and still prints a partial summary marked `INTERRUPTED` (exit 130).

Tested against ansible-core 2.16, 2.20 and 2.21.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), including how to add a fix rule.

## License

[Apache-2.0](LICENSE)
