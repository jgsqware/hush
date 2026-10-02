#!/usr/bin/env bash
# Record a test fixture from a real run against the examples lab.
#   scripts/record-fixture.sh <name> <ansible-playbook args...>
# Writes testdata/lab/<name>/events.jsonl (callback events) and stderr.txt,
# using the same layout as testdata/runs/. ansible-playbook runs inside examples/.
# Override the binary with ANSIBLE_PLAYBOOK (e.g. to test another ansible-core).
set -uo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
name="$1"; shift
out="$root/testdata/lab/$name"
mkdir -p "$out"
cd "$root/examples"
PYTHONDONTWRITEBYTECODE=1 \
ANSIBLE_CALLBACK_PLUGINS="$root/internal/callback" \
ANSIBLE_STDOUT_CALLBACK=hush \
HUSH_EVENT_FD=3 \
PYTHONUNBUFFERED=1 \
${ANSIBLE_PLAYBOOK:-ansible-playbook} "$@" </dev/null \
  3>"$out/events.jsonl" \
  >"$out/stdout.txt" \
  2>"$out/stderr.txt"
rc=$?
# Anonymize: fixtures are committed, local paths must not leak.
sed -i "s|$root|/work/hush|g; s|$HOME|/home/user|g" "$out"/*.txt "$out/events.jsonl" 2>/dev/null
echo "$name: rc=$rc events=$(wc -l <"$out/events.jsonl")"
if [[ -s "$out/stdout.txt" ]]; then echo "  WARNING: unexpected stdout"; else rm -f "$out/stdout.txt"; fi
