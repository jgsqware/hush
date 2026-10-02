#!/usr/bin/env bash
# Start the lab: two Debian containers reachable over SSH on 127.0.0.1:2221/2222.
# Idempotent: quick no-op when the lab is already up.
set -euo pipefail
cd "$(dirname "$0")"
[[ -f id_ed25519 ]] || ssh-keygen -q -t ed25519 -N '' -C hush-lab -f id_ed25519
docker compose build -q
docker compose up -d --wait --quiet-pull 2>&1 | grep -v -E '^\s*(Container|Network) .* (Running|Healthy|Started|Created|Waiting)\s*$' || true
echo "lab up: web-01 127.0.0.1:2221, mon-01 127.0.0.1:2222"
