#!/usr/bin/env bash
# Linux CI smoke test of the exact user-facing installer. Only test credentials
# and deterministic models; a fresh project and volumes are removed on exit.
set -euo pipefail
cd "$(dirname "$0")/.."
root="$PWD"
project="casecite-installtest-$$"
state="$root/.casecite-test-$$"
image="${INSTALLER_TEST_IMAGE:-casecite-packaging:test}"
provider="$project-provider"
app_port="${INSTALLER_TEST_APP_PORT:-29993}"
mkdir -m 700 "$state"
compose=(docker compose -p "$project" --env-file "$state/.env" -f docker-compose.yml -f deploy/compose.install.yml)
cleanup() {
  result=$?
  docker rm -f "$provider" >/dev/null 2>&1 || true
  if [ -f "$state/.env" ]; then
    if [ "$result" -ne 0 ]; then "${compose[@]}" logs --tail 30 app goodmem; fi
    "${compose[@]}" --profile setup down -v --remove-orphans
  fi
  docker network rm "${project}_default" >/dev/null 2>&1 || true
  rm -rf -- "$state"
  exit "$result"
}
trap cleanup EXIT
export CASECITE_IMAGE="$image" CASECITE_INSTALL_DIR="$state"
# The deterministic provider joins the disposable project's private network;
# it publishes no host port and requires neither paid APIs nor a local GPU.
docker network create --label "com.docker.compose.project=$project" --label com.docker.compose.network=default --label "ai.pairsys.casecite.install-dir=$state" "${project}_default" >/dev/null
docker run -d --name "$provider" --network "${project}_default" --network-alias fixture-provider casecite-installer-provider:test >/dev/null
export OPENAI_API_KEY='fixture-$UNEXPANDED-"quote"-\slash'
export VOYAGE_API_KEY=fixture-voyage-only
export OPENAI_BASE_URL=http://fixture-provider:8080/v1
export EMBEDDING_BASE_URL="$OPENAI_BASE_URL" VOYAGE_BASE_URL="$OPENAI_BASE_URL"
export EMBEDDING_MODEL=test-embedding EMBEDDING_DIMENSIONS=16 LLM_MODEL=test-model
export VOYAGE_EMBEDDING_MODEL=test-voyage VOYAGE_EMBEDDING_DIMENSIONS=16
export GOODMEM_IDENTITY_CAPACITY=4
bash scripts/casecite.sh install --state-dir "$state" --project "$project" --port "$app_port" --image "$image" --ocr-url http://fixture-provider:8080/ocr/v1 --non-interactive --no-pull
cp "$state/.env" "$state/env-before"
# Verify dotenv round-trips a key with dollars, quotes and backslashes without
# letting the host environment mask broken escaping in the saved configuration.
unset OPENAI_API_KEY VOYAGE_API_KEY
"${compose[@]}" run --rm --no-deps --entrypoint sh setup -c 'test "$OPENAI_API_KEY" = '\''fixture-$UNEXPANDED-"quote"-\slash'\'''
"${compose[@]}" run --rm --no-deps --entrypoint cat setup /data/goodmem.json > "$state/runtime-before.json"
node -e 'const s=require(process.argv[1]); if(s.security_version!==1 || !s.reranker_id || s.embedder_ids.length!==2 || s.subjects.length!==4) process.exit(1)' "$state/runtime-before.json"
curl --fail --silent "http://localhost:$app_port/ready" >/dev/null
curl --fail --silent "http://localhost:$app_port/word-addin/" >/dev/null
node scripts/test-installed-app.mjs "http://localhost:$app_port" "$state" create
# A second invocation must preserve secrets, model registrations and identities.
bash scripts/casecite.sh stop --state-dir "$state"
bash scripts/casecite.sh start --state-dir "$state" --no-pull
cmp "$state/.env" "$state/env-before"
"${compose[@]}" run --rm --no-deps --entrypoint cat setup /data/goodmem.json > "$state/runtime-after.json"
cmp "$state/runtime-before.json" "$state/runtime-after.json"
node scripts/test-installed-app.mjs "http://localhost:$app_port" "$state" verify
# Even after a container stop, another state directory must not adopt the data.
if bash scripts/casecite.sh install --state-dir "$state/foreign" --project "$project" --port "$app_port" --image "$image" --ocr-url http://fixture-provider:8080/ocr/v1 --non-interactive --no-pull > "$state/collision.log" 2>&1; then
  echo 'ERROR: installer adopted another installation' >&2
  exit 1
fi
grep -q 'belongs to another installation' "$state/collision.log"
echo 'Installer passed: fresh provisioning, dotenv escaping, readiness, add-in, restart, credential stability and project isolation.'
