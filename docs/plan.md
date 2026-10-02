# Plan reference

`hush run plan.yaml` runs the playbooks of a plan, section by section, and ends with one
summary for the whole plan. Everything runs in the plan file's directory, so relative paths
are relative to it. Unknown keys are errors.

```yaml
before:
  - name: devault
    run: ./scripts/lab-devault.sh
    summary_regex: '(\d+) bloc.* dans (\d+) fichier'
    warn_regex: '^\s+AVERTISSEMENT (.+)$'
guard:
  allowed_cidrs: [192.0.2.0/24]
  inventory: inventories/lab.yaml
env:
  ANSIBLE_CONFIG: ansible.cfg
sections:
  - name: Docker hosts + Caddy
    playbooks:
      - { path: servers/docker/deploy_full.yaml, limit: "web-01,mon-01" }
      - { path: tools/caddy/deploy.yaml, limit: "web-01,mon-01", args: ["--diff"] }
fail_fast: true
```

## `before`

Commands run, in order, before anything else. Each runs with `sh -c` and the plan's `env`,
and its output collapses into one line:

```text
 ✓ before devault  1125 · 302                                    0.4s
   ⚠ tools/passbolt/vars/passbolt.yaml : YAML déjà invalide dans le dépôt
```

| Key | Required | |
|---|---|---|
| `name` | yes | shown on the line |
| `run` | yes | shell command |
| `summary_regex` | | the groups of the first matching line are shown on the hook's line |
| `warn_regex` | | every matching line is shown as a warning (group 1 if any, else the line) |

When a hook exits non-zero, hush shows the end of its output and stops: no playbook runs,
and the exit code is the hook's.

## `guard`

A safety check before any playbook runs. hush lists the inventory with
`ansible-inventory --list` and checks every host against `allowed_cidrs`: the host's
`ansible_host` when set, otherwise its name, resolved through DNS. Every address must be in
an allowed range.

| Key | Required | |
|---|---|---|
| `allowed_cidrs` | yes | IPv4 and IPv6 ranges, e.g. `[192.0.2.0/24, "fd00::/8"]` |
| `inventory` | | passed to `ansible-inventory -i`; empty uses the one from `ansible.cfg` |

hush refuses to run (exit 1) when a host is outside the ranges, cannot be resolved, or the
inventory is empty or unreadable.

## `env`

Environment variables for hooks, the guard and every `ansible-playbook` run.

## `sections`

| Key | Required | |
|---|---|---|
| `name` | yes | section title |
| `playbooks` | yes | list of playbooks, run in order |

Each playbook:

| Key | Required | |
|---|---|---|
| `path` | yes | playbook file |
| `limit` | | passed as `--limit` |
| `args` | | extra `ansible-playbook` arguments, e.g. `["-i", "inventories/lab.yaml", "--diff"]` |

## `fail_fast`

When `true`, the plan stops after the first playbook that exits non-zero; the remaining
ones are listed as not run. When `false` (the default), every playbook runs.

## Exit code

The first non-zero `ansible-playbook` exit code, 1 when a before hook or the guard stops the
plan (or the hook's own code), 130 when interrupted, else 0.

## Logs

One file per plan run, `<log-dir>/<time>-<plan>.jsonl`, with the events of every playbook
and of hush itself (hooks, guard, sections). `hush replay` on it prints the same output as
the live run.
