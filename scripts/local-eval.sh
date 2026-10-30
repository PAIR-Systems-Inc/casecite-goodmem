#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [ ! -f .local-eval/.env ]; then
  echo 'Missing private .local-eval/.env. See docs/local-evaluation.md.' >&2
  exit 1
fi
exec docker compose -p casecite-review --env-file .local-eval/.env \
  -f docker-compose.yml -f deploy/compose.local.yml "$@"
