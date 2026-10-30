# Sourced by casecite.sh after argument parsing. Read-only host diagnostics;
# the existing app binary performs network checks inside its own container.
doctor_pass=0 doctor_warn=0 doctor_fail=0
report() {
  case "$1" in
    PASS) doctor_pass=$((doctor_pass+1)) ;;
    WARN) doctor_warn=$((doctor_warn+1)) ;;
    FAIL) doctor_fail=$((doctor_fail+1)) ;;
  esac
  printf '%-4s  %s\n' "$1" "$2"
}
finish() {
  printf '\nHost: %s passed, %s warnings, %s failures.\n' "$doctor_pass" "$doctor_warn" "$doctor_fail"
  [ "$doctor_fail" -eq 0 ] || exit 1
  exit 0
}
echo 'CaseCite by PairSys — doctor (no installation changes)'
# Canonicalize even a nonexistent state directory without creating it.
case "$state" in /*) ;; *) state="$root/$state" ;; esac
ancestor="$state" suffix=""
while [ ! -d "$ancestor" ]; do
  suffix="/$(basename "$ancestor")$suffix"
  ancestor="$(dirname "$ancestor")"
done
state="$(cd "$ancestor" && pwd -P)$suffix"
existing=false
if [ -f "$state/install.conf" ]; then
  existing=true
  [ "$install_options" = false ] && [ "$image_given" = false ] || fail 'Doctor uses the saved settings; pass only --state-dir and optionally --probe-providers.'
  if ! { IFS= read -r project && IFS= read -r port && IFS= read -r mode && IFS= read -r ocr_url && IFS= read -r gpu_id && IFS= read -r image; } < "$state/install.conf"; then
    report FAIL 'Incomplete install.conf. Restore it from backup; do not regenerate secrets.'
    finish
  fi
else
  if [ -e "$state/.env" ]; then
    report FAIL 'Private .env exists without install.conf. Preserve it and use a new --state-dir or restore the missing metadata.'
  else
    report WARN 'Not installed yet. These are host preflight checks; run install afterward.'
  fi
fi
if ! [[ "$project" =~ ^[a-z0-9][a-z0-9_-]*$ && "$port" =~ ^[0-9]{4,5}$ && "$gpu_id" =~ ^[0-9]+$ ]] || [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
  report FAIL 'Invalid project, port or GPU setting. Check options or restore install.conf.'
  finish
fi
case "$project" in casecite-review|casecite-goodmem|goodmem) report FAIL 'That project name is reserved. Choose a separate --project.'; finish ;; esac
case "$mode" in gpu|gpu-cache|external) ;; *) report FAIL 'Invalid OCR mode in install.conf.'; finish ;; esac
if ! [[ "$ocr_url" =~ ^https?://[^[:space:]\"\'\`\$]+/v1$ ]] || [[ "$ocr_url" = *'@'* ]]; then
  report FAIL 'OCR URL must be an HTTP(S) /v1 endpoint without credentials.'
  finish
fi
if ! command -v docker >/dev/null; then
  report FAIL 'Docker is missing. Install Docker Engine/Desktop and Compose; see docs/quickstart.md.'
  finish
fi
if ! docker info >/dev/null 2>&1; then
  report FAIL 'Docker is unavailable. Start Docker and check that your account can access its socket.'
  finish
fi
report PASS 'Docker daemon is accessible.'
compose_version="$(docker compose version --short 2>/dev/null || true)"
if awk -v v="${compose_version#v}" 'BEGIN { split(v,a,"."); exit !(a[1]>2 || (a[1]==2 && (a[2]>24 || (a[2]==24 && a[3]>=4)))) }'; then
  report PASS "Docker Compose $compose_version meets the minimum version."
else
  report FAIL 'Docker Compose >= 2.24.4 is required. Update Docker Desktop or the Compose plugin.'
fi
owned=true
for kind in container volume network; do
  case "$kind" in
    container) ids="$(docker ps -aq --filter "label=com.docker.compose.project=$project")"; template='{{index .Config.Labels "ai.pairsys.casecite.install-dir"}}' ;;
    *) ids="$(docker "$kind" ls -q --filter "label=com.docker.compose.project=$project")"; template='{{index .Labels "ai.pairsys.casecite.install-dir"}}' ;;
  esac
  for id in $ids; do
    owner="$(docker "$kind" inspect --format "$template" "$id")"
    if [ "$owner" != "$state" ]; then owned=false; fi
  done
done
if [ "$owned" = false ]; then
  report FAIL 'This Docker project belongs to another installation. Choose a different --project and --state-dir.'
  finish
fi
report PASS 'Project ownership is isolated from other installations.'
app_id="$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service=app)"
ocr_id="$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service=ocr)"
host_info="$(docker info --format '{{.OSType}}|{{.Architecture}}|{{.MemTotal}}|{{.DockerRootDir}}|{{json .Runtimes}}')"
IFS='|' read -r host_os host_arch host_ram docker_root runtimes <<< "$host_info"
if [[ "$host_ram" =~ ^[0-9]+$ ]] && [ "$host_ram" -ge 15000000000 ]; then
  report PASS 'Docker has at least approximately 16 GB RAM available.'
else
  report WARN 'Docker has less than 16 GB RAM. Increase its memory allocation for local OCR; external OCR needs less.'
fi
docker_endpoint="${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)}"
local_engine=false
case "$docker_endpoint" in unix://*) local_engine=true ;; esac
if [ "$local_engine" = true ]; then
  disk_path="$ancestor"
  if [ -d "$docker_root" ]; then disk_path="$docker_root"; fi
  disk_kb="$(df -Pk "$disk_path" 2>/dev/null | awk 'END {print $4}')"
  if [[ "$disk_kb" =~ ^[0-9]+$ ]] && [ "$disk_kb" -ge 41943040 ]; then
    report PASS 'Host filesystem has at least 40 GiB free for images, weights and data.'
  else
    report WARN 'Less than 40 GiB free or disk space could not be measured. Allow about 40 GB for a fresh local OCR install; cached images/external OCR need less.'
  fi
  if [ ! -d "$docker_root" ]; then report WARN 'Docker uses a VM or inaccessible data directory. Also check free space inside Docker Desktop.'; fi
else
  report WARN 'Remote Docker context: disk space, host ports and GPU availability must be checked on that Docker host.'
fi
if [ -n "$app_id" ]; then
  report PASS "Application container is running (configured localhost port $port)."
elif [ "$local_engine" = true ] && command -v ss >/dev/null; then
  if [ -n "$(ss -H -ltn "sport = :$port" 2>/dev/null)" ]; then
    report FAIL "Port $port is already in use. Choose another --port for a new installation; inspect the listener before restarting an existing one."
  else
    report PASS "Host port $port is available."
  fi
elif [ "$local_engine" = true ] && command -v lsof >/dev/null; then
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    report FAIL "Port $port is already in use. Choose another --port."
  else
    report PASS "No visible listener on host port $port."
  fi
else
  report WARN 'Port availability was not checked; the installer will verify the bind.'
fi
if [ "$mode" = external ]; then
  report PASS 'External OCR selected; no local GPU or OCR image is required.'
  if [ "$existing" = false ]; then report WARN 'OCR model availability will be verified from inside Docker during installation.'; fi
else
  if [ "$host_os/$host_arch" != linux/x86_64 ]; then
    report FAIL 'Local OCR requires a Linux x86-64 Docker host. Use --ocr-url for an existing compatible service.'
  elif [ "$local_engine" != true ]; then
    report FAIL 'Local GPU diagnostics require running this command on the Docker host. Use a local Docker context or --ocr-url.'
  elif ! command -v nvidia-smi >/dev/null; then
    report FAIL 'NVIDIA drivers/nvidia-smi are missing. Install the drivers and Container Toolkit, or use --ocr-url.'
  else
    gpu_info="$(nvidia-smi -i "$gpu_id" --query-gpu=memory.total,memory.free,compute_cap --format=csv,noheader,nounits 2>/dev/null || true)"
    if ! awk -F, 'NF==3 {ok=($1>=8000 && $3>=8.0)} END {exit !ok}' <<< "$gpu_info"; then
      report FAIL 'Selected GPU needs >= 8 GB VRAM and compute capability >= 8.0. Check --gpu or use --ocr-url.'
    elif [ -n "$ocr_id" ]; then
      report PASS 'Compatible GPU; this installation already has OCR running. Its allocated VRAM is expected.'
    elif awk -F, 'NF==3 {ok=($2>=($1*0.68+300))} END {exit !ok}' <<< "$gpu_info"; then
      report PASS 'GPU has enough free VRAM for the bundled OCR profile.'
    else
      report FAIL 'Not enough free GPU memory: OCR needs 68% of total VRAM plus 300 MiB free. Free memory or use an existing OCR endpoint; doctor will not stop workloads.'
    fi
    if [[ "$runtimes" = *'"nvidia"'* ]] || [ -n "$ocr_id" ]; then
      report PASS 'NVIDIA container runtime is registered or this installation already runs OCR.'
    else
      report WARN 'NVIDIA runtime not listed. Check Container Toolkit configuration; the installer will test GPU access in its OCR container.'
    fi
  fi
  if [ -n "$cache" ] && [ ! -d "$cache/models--rednote-hilab--dots.ocr/snapshots/c0111ce6bc07803dbc267932ffef0ae3a51dc951" ]; then
    report FAIL 'Model cache lacks the pinned dots.ocr snapshot. Use the Hugging Face hub directory or omit --model-cache to download it.'
  fi
fi
if [ "$existing" = true ]; then
  for private_path in "$state" "$state/.env" "$state/install.conf"; do
    permissions="$(stat -c '%a' "$private_path" 2>/dev/null || stat -f '%Lp' "$private_path" 2>/dev/null || true)"
    if [[ "$permissions" =~ ^[0-7]+$ ]] && [ $((8#$permissions & 077)) -eq 0 ]; then
      report PASS "Private permissions: $(basename "$private_path")."
    else
      report FAIL "Missing or non-private $(basename "$private_path"). Restore the file or set directory permissions to 700 and files to 600."
    fi
  done
  if [ -d "$state/.lock" ]; then report WARN 'Installer lock exists. Check for an active installer before removing a stale lock.'; fi
  if [ -z "$app_id" ]; then
    report FAIL 'Application is stopped or missing. Run bash scripts/casecite.sh start with this --state-dir, then rerun doctor.'
  else
    app_image="$(docker inspect --format '{{.Image}}' "$app_id")"
    capability="$(docker image inspect --format '{{index .Config.Labels "ai.pairsys.casecite.doctor"}}' "$app_image")"
    if [ "$capability" != 1 ]; then
      report FAIL 'This app image predates doctor. Run bash scripts/casecite.sh update with this --state-dir, then rerun doctor.'
    else
      runtime_args=(doctor)
      if [ "$probe_providers" = true ]; then runtime_args+=(--probe-providers); fi
      echo
      if docker exec "$app_id" casecite "${runtime_args[@]}"; then
        report PASS 'Application runtime diagnostics completed.'
      else
        report FAIL 'Runtime diagnostics failed; follow the fixes above and rerun doctor.'
      fi
    fi
  fi
else
  report WARN 'Have an OpenAI key ready for the hidden install prompt; Voyage is optional. Keys are not printed or checked in preflight.'
  if [ "$probe_providers" = true ]; then report FAIL 'Provider probes need a running installation. Install first, then rerun doctor --probe-providers.'; fi
fi
finish
