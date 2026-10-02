# hush testdata

Fixtures replaying a real 8-playbook lab run (2026-10-01), anonymized, plus synthetic failure variants.
Regenerate with `python3 gen_fixtures.py testdata` (script at repo root, deterministic).

## Layout

```
plan.yaml                     plan reproducing the run (3 sections, 8 playbooks)
inventory.yaml                web-01 (192.0.2.10), mon-01 (192.0.2.11)
hooks/devault.out             before-hook output (banner summary + 2 warnings)
hooks/guard.out               CIDR guard output
expected-plan-summary.yaml    global summary: totals per host, dedup'd warnings, verdict
runs/NN-<name>/
  events.jsonl                hush callback stream (schema v1, see below)
  stderr.txt                  ansible stderr: [WARNING] / [DEPRECATION WARNING] blocks, verbatim format
  stderr.timeline.jsonl       where each stderr block lands relative to events (for live interleaving tests)
  expected.yaml               renderer-agnostic assertions: recap, changed tasks (+ items x/y), failures
variants/
  caddy-failed/               Restart caddy fails on mon-01, exit 2
  loki-unreachable/           mon-01 unreachable at Gathering Facts, exit 4
```

## Event schema v1

Every line: `{"v":1,"ts":"<RFC3339 ms>","event":"<kind>", ...}`

| event | payload |
|---|---|
| `playbook_start` | `playbook{path,rel}`, `options{limit,check,diff,tags,skip_tags}`, `ansible{core,python}` |
| `play_start` | `play{uuid,name,pattern,hosts[],strategy}` |
| `task_start` | `task{uuid,name,action,path,line,is_handler,play_uuid}` (path/line null for gather_facts) |
| `item_ok` / `item_skipped` | `host`, `task_uuid`, `item{label,changed}`, emitted before the final runner event |
| `runner_ok` | `host`, `task_uuid`, `duration_ms`, `delegated_to`, `ignore_errors`, `result{changed,msg?,results_count?}` |
| `runner_skipped` | same + `result{skip_reason,false_condition}` |
| `runner_failed` | same + `result{failed:true,msg,stderr}` |
| `runner_unreachable` | same + `result{unreachable:true,msg}` |
| `stats` | `stats{<host>{ok,changed,failures,unreachable,skipped,rescued,ignored}}` (Ansible semantics: `ok` includes changed) |
| `playbook_end` | `exit_code`, `duration_ms` |

Meta tasks (e.g. `flush_handlers`) emit `task_start` with no runner events.

## Anonymization

Host names, addresses (TEST-NET-1, 192.0.2.0/24), users, client paths (/work/infra) and
application, image and group names were genericized. The mapping is not published.

## Schema additions (callback plugin, milestone 1)

Found necessary while running the real plugin against ansible-core 2.16 and 2.21
(see `internal/callback/hush.py`). All optional; decoders ignore what they don't know.

| event | payload |
|---|---|
| `runner_start` | `host`, `task_uuid` — a host started the task (pending hosts, free strategy) |
| `item_failed` | like `item_ok`, plus `item.msg` |
| `runner_retry` | `host`, `task_uuid`, `attempts`, `retries` |
| `include` | `file`, `hosts[]`, `task_uuid` |
| `handler_notified` | `handler`, `host` |
| `no_hosts_matched` / `no_hosts_remaining` | — |
| `warning` / `deprecation` | `source:"task"`, `host`, `task_uuid`, `msg`, `origin?`, `help?`, `version?`, `date?`, `collection?` |

Extra `result` fields: `stdout`, `stderr` (max 200 lines) with `stdout_lines_total` / `stderr_lines_total`,
`rc`, `has_diff`, `debug` (debug module output), `rescuable` (failure inside a block with `rescue`).
`play.check_mode` is added to `play_start`.

Written by hush, not the plugin: `playbook_end` (only hush knows the exit code) and
`stderr` (`line`: one line of the child's stderr, interleaved live).

Behavior notes verified on real runs:
- 2.19+ captures warnings raised during a task (including ssh `sftp/scp transfer mechanism failed`)
  onto the task result: they arrive as `warning`/`deprecation` events and **not** on stderr.
  2.16 prints them on stderr, wrapped at ~80 columns. Both paths must feed the same dedup.
- Free strategy emits `task_start` once per host for the same task uuid.
- 2.19+ prefixes unreachable messages with `Task failed: ` (stripped by the decoder).
- A literal `when: false` makes `false_condition` a boolean; the plugin stringifies it.
- A failed task reports `changed: true` for command/shell; do not count it as changed.

## Lab fixtures

`lab/<ansible-version>-<playbook>/{events.jsonl,stderr.txt}`: real runs of `examples/playbooks/*`
against the docker lab (`examples/lab/up.sh`), recorded with `scripts/record-fixture.sh`.
mon-01 has no sftp server on purpose, to produce transfer warnings. ghost-01 is unreachable.

## Plan events (milestone 5)

`hush run` writes one log per plan: the callback events of every playbook, plus these events
written by hush (all optional for decoders):

| event | payload |
|---|---|
| `command` | `argv[]`: the ansible-playbook command about to run (rerun hints in replays) |
| `hook` | `hook{name,exit_code,duration_ms,summary[],warnings[],tail[]}` |
| `guard` | `guard{ok,hosts,cidrs[],offenders[],error}` |
| `section` | `section{index,count,name}` |
| `not_run` | `skipped[]` playbook paths, `reason` (fail_fast, interrupted, before hook failed, guard refused) |

`playbook_end` gains `interrupted: true` when hush stopped the run (SIGINT/SIGTERM).
`inventory.list.json` is `ansible-inventory --list` of `inventory.yaml`, for guard tests.
`plan.plain.golden` and `plan.ci-github.golden` render `plan.yaml` replayed from `runs/`.
