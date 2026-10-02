# Contributing to hush

## Setup

The toolchain is pinned in `mise.toml` (Go, Python, uv, ansible-core, golangci-lint,
goreleaser, syft, vhs). With [mise](https://mise.jdx.dev):

```sh
mise install
mise tasks          # everything below is a task
```

| Task | |
|---|---|
| `mise run build` | build `bin/hush` |
| `mise run check` | what CI runs: gofmt, go vet, golangci-lint, tests with `-race` |
| `mise run test:update` | regenerate golden files, then review `git diff testdata/` |
| `mise run lab:run [site\|fail\|free\|warnings]` | run a lab playbook through hush (starts the lab) |
| `mise run lab:plan` | run `examples/plan.yaml` |
| `mise run lab:record` | re-record the lab fixtures on ansible-core 2.21 and 2.16 |
| `mise run lab:down` | stop the lab |

The lab (`examples/lab/`) is two Debian containers reachable over SSH. `mon-01` has no sftp
server on purpose (transfer warnings) and `ghost-01` is unreachable.

## Tests

- **Unit tests** next to the code. The aggregator (`internal/agg`) is pure: build a
  synthetic event stream and assert on the items it returns.
- **Golden tests**: every fixture in `testdata/*/*/events.jsonl` is replayed through each
  render mode (plain, GitLab CI, GitHub CI, tty with a fake clock) and compared with the
  `.golden` file next to it. After a deliberate output change, run `mise run test:update`
  and read the diff before committing.
- **Integration test**: real ansible-core against two containers, behind a build tag:

  ```sh
  go test -tags integration ./test/integration/
  ```

  It needs Docker, `ssh-keygen` and `ansible-playbook` in `PATH`.

When Ansible's behavior is unclear, check it with a real run in the lab instead of guessing,
then record a fixture: `scripts/record-fixture.sh <name> playbooks/<file>.yaml`.

## Adding a fix rule

Known warnings and their fixes live in
[`internal/warnings/fixes.yaml`](internal/warnings/fixes.yaml). The first matching rule
wins, so put specific rules before general ones.

1. Get the exact warning text, ideally from a real run (`hush replay` a log, or
   `ansible-playbook … 2>&1 | grep -A3 WARNING`).
2. Add a rule:

   ```yaml
   - id: inventory.no_match          # dedup key; {1}… are groups of match
     kind: warning                   # warning | deprecation | any
     match: 'Could not match supplied host pattern'
     tag: inventory                  # short column (deprecations default to the version)
     label: host pattern matched nothing
     fix: "check --limit and the play's hosts: pattern"
   ```

   A rule can also read the source snippet that ansible-core 2.19+ prints under
   `Origin:`: `context` is a regexp on it, and its groups are `{c1}`, `{c2}`… in `fix`
   (`fix_default` is used when it does not match). `hide: true` counts a warning without
   showing it.
3. Add a test in `internal/warnings/warnings_test.go` with the real message, run
   `mise run test:update` if goldens change, and check `mise run check`.

Users can add rules without rebuilding hush in `~/.config/hush/fixes.yaml`; rules there are
checked before the built-in ones.

## Style

Boring, idiomatic Go: small packages, no premature abstraction, comments that say why.
Keep commits focused, and describe user-visible changes in `CHANGELOG.md`.
