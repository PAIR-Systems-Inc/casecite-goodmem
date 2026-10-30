#!/usr/bin/env bash
# Portable prebuilt install and lifecycle commands. Never sources a dotenv file,
# builds locally, touches another Compose project, or removes persistent data.
set -euo pipefail
umask 077
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
command_name="${1:-help}"
if [ "$#" -gt 0 ]; then shift; fi
state="$root/.casecite"
project=casecite-demo
port=8000
mode=gpu
ocr_url=http://ocr:8000/v1
gpu_id=0
image=ghcr.io/pair-systems-inc/casecite-goodmem:demo
cache=""
interactive=true
pull=true
image_given=false
install_options=false
fail() { echo "CaseCite: $*" >&2; exit 1; }
usage() {
  cat <<'EOF'
CaseCite by PairSys — prebuilt Docker installation

  bash scripts/casecite.sh install [options]
  bash scripts/casecite.sh start|stop|status|logs|token [--state-dir PATH]
  bash scripts/casecite.sh update [--image IMAGE_OR_DIGEST] [--state-dir PATH]

Install options:
  --ocr-url URL        Use an existing dots.ocr-compatible /v1 endpoint instead
                      of a local NVIDIA GPU (reachable from Docker containers).
  --model-cache PATH   Reuse a Hugging Face hub cache read-only; otherwise the
                      pinned OCR weights download into a new Docker volume.
  --gpu ID            NVIDIA GPU index (default 0).
  --project NAME      Unique Compose project (default casecite-demo).
  --port PORT         Localhost application port (default 8000).
  --state-dir PATH    Private configuration directory (default .casecite).
  --image IMAGE       App image/tag/digest (default GHCR :demo).
  --non-interactive   Read OPENAI_API_KEY and optional VOYAGE_API_KEY from env.
  --no-pull           Use cached images only (development/air-gapped installs).

Requires Docker Engine/Desktop and Compose >= 2.24.4. Local GPU OCR requires
Linux x86-64, NVIDIA Container Toolkit, an Ampere-or-newer GPU with >= 8 GB VRAM,
and sufficient free VRAM. External OCR also works without a local NVIDIA GPU.
EOF
}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --state-dir|--project|--port|--image|--ocr-url|--model-cache|--gpu)
      [ "$#" -ge 2 ] && [ -n "$2" ] || fail "Missing value for $1"
      case "$1" in
        --state-dir) state="$2" ;;
        --project) project="$2"; install_options=true ;;
        --port) port="$2"; install_options=true ;;
        --image) image="$2"; image_given=true ;;
        --ocr-url) ocr_url="${2%/}"; mode=external; install_options=true ;;
        --model-cache) cache="$2"; install_options=true ;;
        --gpu) gpu_id="$2"; install_options=true ;;
      esac
      shift 2 ;;
    --non-interactive) interactive=false; shift ;;
    --no-pull) pull=false; shift ;;
    --help|-h) usage; exit 0 ;;
    *) fail "Unknown option: $1" ;;
  esac
done
case "$command_name" in
  help|-h|--help) usage; exit 0 ;;
  install|start|stop|status|logs|token|update) ;;
  *) usage; fail "Unknown command: $command_name" ;;
esac
command -v docker >/dev/null || fail "Install Docker and its Compose plugin first. See docs/quickstart.md."
docker info >/dev/null 2>&1 || fail "Docker is not running or your account cannot access it."
compose_version="$(docker compose version --short)"
if ! awk -v v="${compose_version#v}" 'BEGIN { split(v,a,"."); exit !(a[1]>2 || (a[1]==2 && (a[2]>24 || (a[2]==24 && a[3]>=4)))) }'; then
  fail "Docker Compose >= 2.24.4 is required (found $compose_version)."
fi
if [ "$command_name" != install ]; then [ -f "$state/install.conf" ] || fail "No installation at $state; run install first."; fi
mkdir -p "$state"
state="$(cd "$state" && pwd)"
chmod 700 "$state"
if ! mkdir "$state/.lock" 2>/dev/null; then fail "Another installer is using $state. If it was interrupted, remove its .lock directory after checking no installer is running."; fi
cleanup() { rmdir "$state/.lock"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
candidate_image="$image"
existing=false
if [ -f "$state/install.conf" ]; then
  existing=true
  [ "$install_options" = false ] || fail "Installation settings already exist; use start, or a different --state-dir for another installation."
  {
    IFS= read -r project
    IFS= read -r port
    IFS= read -r mode
    IFS= read -r ocr_url
    IFS= read -r gpu_id
    IFS= read -r image
  } < "$state/install.conf"
  if [ "$image_given" = true ] && [ "$command_name" != update ]; then fail "Use update --image to change an existing installation."; fi
fi
[[ "$project" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || fail "Project must contain lowercase letters, numbers, hyphens or underscores."
case "$project" in casecite-review|casecite-goodmem|goodmem) fail "Choose a separate project name; $project is reserved for existing/manual installations." ;; esac
[[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -ge 1024 ] && [ "$port" -le 65535 ] || fail "Port must be 1024–65535."
[[ "$gpu_id" =~ ^[0-9]+$ ]] || fail "GPU ID must be a numeric device index."
case "$mode" in gpu|gpu-cache|external) ;; *) fail "Invalid stored OCR mode." ;; esac
[[ "$ocr_url" =~ ^https?://[^[:space:]\"\'\`\$]+/v1$ ]] && [[ "$ocr_url" != *'@'* ]] || fail "OCR URL must be an HTTP(S) /v1 endpoint without credentials."
valid_image() { [[ "$1" =~ ^[a-zA-Z0-9][a-zA-Z0-9./_:@-]*$ ]] || fail "Invalid image reference."; }
valid_image "$image"
# Ownership labels prevent a mistyped project/state directory from adopting
# another installation, even when its containers have already been removed.
for id in $(docker ps -aq --filter "label=com.docker.compose.project=$project"); do
  owner="$(docker inspect --format '{{index .Config.Labels "ai.pairsys.casecite.install-dir"}}' "$id")"
  [ "$owner" = "$state" ] || fail "Project $project belongs to another installation; choose --project and --state-dir."
done
for volume in $(docker volume ls -q --filter "label=com.docker.compose.project=$project"); do
  owner="$(docker volume inspect --format '{{index .Labels "ai.pairsys.casecite.install-dir"}}' "$volume")"
  [ "$owner" = "$state" ] || fail "Volume $volume belongs to another installation; choose a different project."
done
for network in $(docker network ls -q --filter "label=com.docker.compose.project=$project"); do
  owner="$(docker network inspect --format '{{index .Labels "ai.pairsys.casecite.install-dir"}}' "$network")"
  [ "$owner" = "$state" ] || fail "Network for $project belongs to another installation; choose a different project."
done
if [ "$existing" = false ]; then
  [ ! -e "$state/.env" ] || fail "$state/.env already exists without install.conf; preserve it and choose a fresh state directory."
  if [ "$mode" = external ] && [ -n "$cache" ]; then fail "--model-cache cannot be combined with --ocr-url."; fi
  if [ -n "$cache" ]; then
    [ -d "$cache" ] || fail "Model cache directory does not exist."
    cache="$(cd "$cache" && pwd)"
    mode=gpu-cache
  fi
  if [ -z "${OPENAI_API_KEY:-}" ]; then
    [ "$interactive" = true ] && [ -t 0 ] || fail "Set OPENAI_API_KEY, or run interactively to enter it."
    read -r -s -p 'OpenAI API key: ' OPENAI_API_KEY
    echo
    export OPENAI_API_KEY
  fi
  [ -n "${OPENAI_API_KEY:-}" ] || fail "An OpenAI API key is required for the default embedding/generation configuration."
  if [ -z "${VOYAGE_API_KEY:-}" ] && [ "$interactive" = true ] && [ -t 0 ]; then
    read -r -s -p 'Voyage API key (optional; Enter skips reranking): ' VOYAGE_API_KEY
    echo
    export VOYAGE_API_KEY
  fi
fi
resolve_image() {
  valid_image "$1"
  if [ "$pull" = true ]; then docker pull "$1" >&2; fi
  docker image inspect "$1" >/dev/null || fail "App image is not cached. Remove --no-pull."
  pinned="$(docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$1" | head -n 1)"
  if [ -n "$pinned" ]; then image="$pinned"; else image="$1"; fi
}
save_metadata() {
  printf '%s\n' "$project" "$port" "$mode" "$ocr_url" "$gpu_id" "$image" > "$state/install.conf.tmp"
  mv "$state/install.conf.tmp" "$state/install.conf"
}
if [ "$existing" = false ]; then
  resolve_image "$image"
  # Docker performs the bind check, including ports occupied outside Docker.
  docker run --rm -p "127.0.0.1:$port:8000" "$image" version >/dev/null || fail "Cannot use localhost:$port; select another --port."
  export PORT="$port" GOODMEM_OCR_BASE_URL="$ocr_url" OCR_GPU_ID="$gpu_id" CASECITE_OCR_CACHE="$cache"
  pass_env=()
  for key in OPENAI_API_KEY VOYAGE_API_KEY OPENAI_BASE_URL LLM_MODEL OPENAI_UTILITY_MODEL EMBEDDING_API_KEY EMBEDDING_BASE_URL EMBEDDING_MODEL EMBEDDING_DIMENSIONS GOODMEM_IDENTITY_CAPACITY VOYAGE_BASE_URL VOYAGE_RERANKER_MODEL VOYAGE_EMBEDDING_MODEL VOYAGE_EMBEDDING_DIMENSIONS PORT GOODMEM_OCR_BASE_URL OCR_GPU_ID CASECITE_OCR_CACHE; do
    pass_env+=(-e "$key")
  done
  docker run --rm --user "$(id -u):$(id -g)" "${pass_env[@]}" --mount "type=bind,src=$state,dst=/install" "$image" init-env /install/.env
  save_metadata
fi
export CASECITE_IMAGE="$image" CASECITE_INSTALL_DIR="$state" PORT="$port" OCR_GPU_ID="$gpu_id" GOODMEM_OCR_BASE_URL="$ocr_url"
compose=(docker compose --project-directory "$root" -p "$project" --env-file "$state/.env" -f "$root/docker-compose.yml" -f "$root/deploy/compose.install.yml")
if [ "$mode" != external ]; then compose+=(-f "$root/deploy/compose.ocr.yml"); fi
if [ "$mode" = gpu-cache ]; then compose+=(-f "$root/deploy/compose.ocr-cache.yml"); fi
"${compose[@]}" config --quiet
if [ "$pull" = false ]; then
  while IFS= read -r required_image; do
    docker image inspect "$required_image" >/dev/null 2>&1 || fail "Required image $required_image is not cached. Remove --no-pull or preload it."
  done < <("${compose[@]}" config --images | sort -u)
fi
case "$command_name" in
  status) "${compose[@]}" ps; exit 0 ;;
  logs) "${compose[@]}" logs --tail 100 app goodmem; exit 0 ;;
  stop) "${compose[@]}" stop; exit 0 ;;
  token) awk -F '"' '/^REGISTRATION_BOOTSTRAP_TOKEN=/{print $2}' "$state/.env"; exit 0 ;;
  update)
    resolve_image "$candidate_image"
    printf '%s\n' "$CASECITE_IMAGE" > "$state/previous-image"
    export CASECITE_IMAGE="$image"
    # No administrative provisioning or provider-key rotation on an app update.
    "${compose[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 app
    save_metadata
    echo "Updated CaseCite to $image. Previous image is recorded in $state/previous-image."
    exit 0 ;;
esac
if [ "$mode" != external ]; then
  ocr_running="$("${compose[@]}" ps --status running -q ocr)"
  if [ -z "$ocr_running" ]; then
    [ "$(docker info --format '{{.OSType}}/{{.Architecture}}')" = linux/x86_64 ] || fail "Local GPU OCR needs a Linux x86-64 Docker host. Use --ocr-url for an external OCR service."
    command -v nvidia-smi >/dev/null || fail "Install NVIDIA drivers and Container Toolkit, or use --ocr-url."
    gpu_info="$(nvidia-smi -i "$gpu_id" --query-gpu=memory.total,memory.free,compute_cap --format=csv,noheader,nounits)" || fail "Cannot query GPU $gpu_id."
    if ! awk -F, '{exit !($1>=8000 && $2>=($1*0.68+300) && $3>=8.0)}' <<< "$gpu_info"; then
      fail "GPU $gpu_id needs >= 8 GB total, 68% of its VRAM plus 300 MiB free, and compute capability >= 8.0. Free GPU memory or use an external OCR endpoint. No GPU services were changed."
    fi
    echo 'Checking NVIDIA Container Toolkit and the OCR image…'
    if [ "$pull" = true ]; then "${compose[@]}" pull ocr model-download; fi
    "${compose[@]}" run --rm --no-deps --entrypoint nvidia-smi ocr -L
  fi
  echo 'Downloading/verifying the pinned OCR weights (first install can take a while)…'
  "${compose[@]}" run --rm --no-deps model-download
fi
if [ "$pull" = true ]; then "${compose[@]}" pull postgres goodmem; fi
services=(postgres goodmem)
if [ "$mode" != external ]; then services+=(ocr); fi
"${compose[@]}" up -d --no-build "${services[@]}"
echo 'Waiting for GoodMem…'
"${compose[@]}" run --rm --no-deps setup wait http://goodmem:8080/v1/system/info 240
echo 'Waiting for the OCR endpoint and its pairsys/pairsys.ocr model…'
"${compose[@]}" run --rm --no-deps setup wait "$ocr_url/models" 900 pairsys/pairsys.ocr
if ! "${compose[@]}" run --rm --no-deps --entrypoint sh setup -c 'test -s /data/goodmem.json'; then
  echo 'Provisioning GoodMem models and scoped application identities…'
  "${compose[@]}" run --rm --no-deps setup
fi
"${compose[@]}" up -d --no-build --wait --wait-timeout 180 app
echo
echo "CaseCite by PairSys is ready: http://localhost:$port"
echo "Create the first administrator in the browser. Get its setup token with:"
printf '  bash scripts/casecite.sh token --state-dir %q\n' "$state"
echo "Private configuration: $state/.env"
echo "App image: $image"
