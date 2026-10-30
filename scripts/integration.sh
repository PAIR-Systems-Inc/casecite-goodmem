#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export TEST_PROJECT="${TEST_PROJECT:-casecite-integration}"
compose=(docker compose -p "$TEST_PROJECT" -f deploy/compose.test.yml)
cleanup() {
  status=$?
  if [ -n "${test_security_dir:-}" ]; then rm -rf -- "$test_security_dir"; fi
  if [ "$status" -ne 0 ]; then "${compose[@]}" logs --tail 60 app goodmem; fi
  if [ "${KEEP_TEST_STACK:-false}" != true ]; then "${compose[@]}" down -v --remove-orphans; fi
  exit "$status"
}
trap cleanup EXIT
"${compose[@]}" build app setup provider
"${compose[@]}" up -d postgres goodmem provider
ready=false
for attempt in {1..60}; do
  if curl -fsS "http://localhost:${TEST_GOODMEM_PORT:-29080}/v1/system/info" >/dev/null; then ready=true; break; fi
  sleep 2
done
if [ "$ready" != true ]; then echo 'GoodMem did not become ready' >&2; exit 1; fi
"${compose[@]}" run --rm setup
test_embedder_id="$("${compose[@]}" run --rm --entrypoint cat setup /provisioning/admin.json | node scripts/test-index-model.mjs "http://localhost:${TEST_GOODMEM_PORT:-29080}")"
"${compose[@]}" run --rm -e "GOODMEM_ALLOWED_EMBEDDER_IDS=$test_embedder_id" setup
# Read-only test copies: never print these credentials or mount provisioning in app.
test_security_dir="$(mktemp -d)"
chmod 700 "$test_security_dir"
"${compose[@]}" run --rm --entrypoint cat setup /data/goodmem.json > "$test_security_dir/runtime.json"
"${compose[@]}" run --rm --entrypoint cat setup /provisioning/admin.json > "$test_security_dir/provisioning.json"
chmod 600 "$test_security_dir/"*.json
(
  cd server
  TEST_DATABASE_URL="postgres://postgres:test-admin-password@127.0.0.1:${TEST_DB_PORT:-29432}/casecite?sslmode=disable" \
  TEST_GOODMEM_BASE_URL="http://localhost:${TEST_GOODMEM_PORT:-29080}" \
  TEST_GOODMEM_SECURITY_CONFIG="$test_security_dir/runtime.json" \
  TEST_GOODMEM_PROVISIONING_FILE="$test_security_dir/provisioning.json" \
  "${GO:-go}" test -race ./internal/app -run '^TestGoodMemSecurityLive$' -count=1
)
"${compose[@]}" up -d app
ready=false
for attempt in {1..30}; do
  if curl -fsS "http://localhost:${TEST_APP_PORT:-29000}/ready" >/dev/null; then ready=true; break; fi
  sleep 2
done
if [ "$ready" != true ]; then echo 'CaseCite did not become ready' >&2; exit 1; fi
E2E_BASE_URL="http://localhost:${TEST_APP_PORT:-29000}" npm --prefix frontend run test:e2e -- --project=chromium
