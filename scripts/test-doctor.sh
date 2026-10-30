#!/usr/bin/env bash
# Host failure paths without Docker mutations, external services or a real GPU.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir "$tmp/bin"
cat > "$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCTOR_TEST_LOG"
case "$*" in
  info) [ "${DOCTOR_TEST_DOWN:-false}" = false ] ;;
  'info --format '*) echo 'linux|x86_64|34359738368|/nonexistent-docker-root|{"nvidia":{}}' ;;
  'compose version --short') echo "${DOCTOR_TEST_COMPOSE:-2.24.4}" ;;
  'context inspect '*) echo unix:///var/run/docker.sock ;;
  'ps -aq '*) ;;
  'ps -q '*service=app) if [ "${DOCTOR_TEST_RUNNING:-false}" = true ]; then echo app-id; fi ;;
  'ps -q '*service=ocr) if [ "${DOCTOR_TEST_RUNNING:-false}" = true ]; then echo ocr-id; fi ;;
  'volume ls '*|'network ls '*) ;;
  'inspect --format {{.Image}} app-id') echo image-id ;;
  'image inspect '*) echo "${DOCTOR_TEST_CAPABILITY:-1}" ;;
  'exec app-id casecite doctor'*) echo 'PASS  Fixture runtime checks'; exit "${DOCTOR_TEST_RUNTIME_EXIT:-0}" ;;
  *) echo "Unexpected Docker command: $*" >&2; exit 99 ;;
esac
EOF
cat > "$tmp/bin/nvidia-smi" <<'EOF'
#!/usr/bin/env bash
echo "${DOCTOR_TEST_GPU:-8192,7000,8.9}"
EOF
cat > "$tmp/bin/ss" <<'EOF'
#!/usr/bin/env bash
if [ "${DOCTOR_TEST_PORT_BUSY:-false}" = true ]; then echo LISTEN; fi
EOF
chmod +x "$tmp/bin/"*
export PATH="$tmp/bin:$PATH" DOCTOR_TEST_LOG="$tmp/docker.log"
state="$tmp/fresh"
run() { bash scripts/casecite.sh doctor --state-dir "$state" "$@" > "$tmp/result" 2>&1; }
expect_fail() { if run "$@"; then cat "$tmp/result"; echo 'Expected doctor failure' >&2; exit 1; fi; }
run --ocr-url http://ocr.example:8000/v1
[ ! -e "$state" ]
DOCTOR_TEST_DOWN=true expect_fail
grep -q 'Docker is unavailable' "$tmp/result"
DOCTOR_TEST_COMPOSE=2.24.3 expect_fail
grep -q 'Compose >= 2.24.4' "$tmp/result"
DOCTOR_TEST_PORT_BUSY=true expect_fail
grep -q 'already in use' "$tmp/result"
DOCTOR_TEST_GPU='8192,1000,8.9' expect_fail
grep -q 'Not enough free GPU memory' "$tmp/result"
DOCTOR_TEST_GPU='8192,7000,7.5' expect_fail
grep -q 'compute capability' "$tmp/result"
[ ! -e "$state" ]
mkdir -m 700 "$state"
printf '%s\n' casecite-demo 8000 gpu http://ocr:8000/v1 0 fixture-image > "$state/install.conf"
printf 'SECRET_KEY="do-not-print"\n' > "$state/.env"
chmod 600 "$state/.env" "$state/install.conf"
cp "$state/.env" "$tmp/env-before"
cp "$state/install.conf" "$tmp/config-before"
DOCTOR_TEST_RUNNING=true DOCTOR_TEST_GPU='8192,1000,8.9' run
grep -q 'allocated VRAM is expected' "$tmp/result"
DOCTOR_TEST_RUNNING=true DOCTOR_TEST_CAPABILITY=0 expect_fail
grep -q 'predates doctor' "$tmp/result"
DOCTOR_TEST_RUNNING=true DOCTOR_TEST_RUNTIME_EXIT=1 expect_fail
grep -q 'Runtime diagnostics failed' "$tmp/result"
expect_fail
grep -q 'Application is stopped' "$tmp/result"
chmod 644 "$state/.env"
DOCTOR_TEST_RUNNING=true expect_fail
grep -q 'non-private .env' "$tmp/result"
cmp "$state/.env" "$tmp/env-before"
cmp "$state/install.conf" "$tmp/config-before"
[ ! -d "$state/.lock" ]
if grep -Eq '^(pull|run|start|stop|rm|compose .* (up|down|pull|run))' "$tmp/docker.log"; then echo 'Doctor mutated Docker' >&2; exit 1; fi
if grep -q do-not-print "$tmp/result"; then echo 'Doctor exposed secrets' >&2; exit 1; fi
echo 'Doctor host checks passed: Docker/Compose, ports, GPU compatibility/occupancy, old images, runtime failures, permissions and no mutations.'
