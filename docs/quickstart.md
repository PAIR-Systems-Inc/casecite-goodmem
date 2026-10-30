# Run CaseCite by PairSys

The portable installer pulls a prebuilt application image from
`ghcr.io/pair-systems-inc/casecite-goodmem:demo`. The Go binary includes the web
interface and Word add-in. **No local Go, Node, Python, or application build is
needed.** Docker runs CaseCite, GoodMem and PostgreSQL as separate services, plus
the existing vLLM image when local GPU OCR is selected.

This is the evaluation channel of the migration branch; the project's documented
parity and live-provider verification limits still apply.

## First install

Install [Docker Engine](https://docs.docker.com/engine/install/) with Compose
**2.24.4 or newer**, or Docker Desktop. Run the commands in Bash (Linux, macOS, or
a suitable Linux environment on Windows). You also need Git to clone the repo.
Have an OpenAI API key ready. A Voyage key is optional and enables retrieval
reranking plus an additional embedding model. Provider usage is billed by the
respective provider; OCR uses your own model service.

For local OCR, use a **Linux x86-64 Docker host**, an NVIDIA Ampere-or-newer GPU
(compute capability at least 8.0) with **at least 8 GB VRAM**, and the
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html).
Allow roughly **40 GB free disk** for the large vLLM image, model weights and
initial data, and **16 GB or more host RAM**. First download can take substantial
time depending on the connection. The installer checks GPU compatibility and
requires 68% of the selected GPU's VRAM plus 300 MiB free before starting OCR.

```bash
git clone --branch goodmem-migration https://github.com/PAIR-Systems-Inc/casecite-goodmem.git
cd casecite-goodmem
bash scripts/casecite.sh install
```

Enter the keys at the hidden prompts. The installer generates independent
database passwords and application secrets, downloads and verifies the pinned
OCR weights, starts its own PostgreSQL/GoodMem/OCR services, registers models,
provisions scoped identities and waits for application readiness. It opens no
GoodMem, PostgreSQL or OCR ports on the host. It does not reuse or change an
existing GoodMem installation.

Open **http://localhost:8000** and create the first administrator. Retrieve the
setup token locally with:

```bash
bash scripts/casecite.sh token
```

Private configuration is in **`.casecite/.env`** (0600 inside a 0700 directory).
Keep it and the Docker volumes backed up. Do not regenerate `SECRET_KEY`: it
encrypts saved credentials. These files are excluded from Git and Docker builds.
The administrative GoodMem credential is in a separate provisioning volume that
the application never mounts.

## No local NVIDIA GPU

The application and GoodMem images support **amd64 and arm64**. Use an existing
GoodMem-compatible Dots OCR service with the served model name
`pairsys/pairsys.ocr`, reachable from the Docker containers:

```bash
bash scripts/casecite.sh install --ocr-url http://ocr-server:8000/v1
```

For an OCR server running directly on the Docker host, use
`http://host.docker.internal:8001/v1` instead of `localhost`. Its listener must
accept connections from Docker. A generic text LLM is not an OCR replacement;
the installer verifies the required model at `/v1/models`. OCR endpoints needing
an API key are not supported by the bundled GoodMem OCR client. Keep that
endpoint on your trusted private network.

This route downloads no vLLM image or OCR weights and starts three services.
The complete installer is tested on Linux amd64. Arm64 application images are
built in CI; a full macOS/Windows/arm64 rehearsal remains necessary on those
machines. Local NVIDIA OCR uses the tested Linux amd64 profile.

## Existing OCR model cache or a second demo

To avoid downloading weights already on disk, provide the **Hugging Face hub
directory**, which contains `models--rednote-hilab--dots.ocr` and its blobs:

```bash
bash scripts/casecite.sh install --model-cache "$HOME/.cache/huggingface/hub"
```

The cache is mounted read-only and every weight shard's SHA-256 is verified.
The required snapshot is `c0111ce6bc07803dbc267932ffef0ae3a51dc951`; a partial or
different snapshot is rejected. Otherwise weights go into a new project-specific
Docker volume. Subsequent starts reuse the files, with no model download during
OCR inference.

Use a different state directory, project and port for another independent demo:

```bash
bash scripts/casecite.sh install --state-dir .casecite-second \
  --project casecite-second --port 18002 --ocr-url http://ocr-server:8000/v1
bash scripts/casecite.sh status --state-dir .casecite-second
```

Sharing the OCR endpoint is possible; running a second local OCR process needs
additional free GPU memory. The installer does not stop other GPU workloads.

## Start, stop and update

```bash
bash scripts/casecite.sh status
bash scripts/casecite.sh logs
bash scripts/casecite.sh stop
bash scripts/casecite.sh start

# Pull the newest tested demo application image. Back up before upgrading.
git pull --ff-only
bash scripts/casecite.sh update
```

The initial pull resolves the app tag to an immutable digest stored in
`.casecite/install.conf`. Restarting uses that digest, preserves secrets and
skips already completed provisioning. `update` changes only the app image and
records the previous image in `.casecite/previous-image`; it does not reprovision
GoodMem or rotate provider credentials. To select a particular tested commit,
use `update --image ghcr.io/pair-systems-inc/casecite-goodmem:sha-FULL_COMMIT_SHA`,
or pass an immutable image digest. Reverting a binary is not a substitute for
restoring a compatible database backup if an upgrade changes its schema.

For offline use, preload all the selected pinned images and OCR weights, then
pass `--no-pull`. This flag does not provide an offline replacement for external
OpenAI/Voyage APIs. Noninteractive installation accepts `OPENAI_API_KEY` and
optional `VOYAGE_API_KEY` from the environment with `--non-interactive`.

Custom OpenAI-compatible endpoint/model/dimensions can be supplied through
`OPENAI_BASE_URL`, `LLM_MODEL`, `EMBEDDING_BASE_URL`, `EMBEDDING_MODEL` and
`EMBEDDING_DIMENSIONS` before the initial installation. After configuration,
avoid exporting conflicting values into your management shell: Compose gives
process environment variables precedence over the saved `.env` file.

## Before the demo

Create the administrator, upload a scan and a native Word/PDF document, wait for
indexing, then rehearse a question with verified citations and page previews.
Test the chosen contract or workflow scenario separately. Pre-index the demo
documents: the 8 GB workstation profile needed about 45 seconds for a short
scanned page in the existing review test. That is not a throughput guarantee for
longer or denser documents.

Google/Microsoft SSO and connectors still require credentials for your own
organization. Add their settings from `.env.example` or the administrator UI;
see [Google SSO](google-sso.md), [connectors](connectors.md) and
[Word add-in](word-addin.md). The bundled add-in assets are served at `/word-addin/`;
Word-host installation and HTTPS requirements are covered in that guide.

## Troubleshooting

- **Docker unavailable / old Compose:** start Docker, grant your account Docker
  access, and update the Compose plugin. No local application build is needed.
- **Port or project already used:** select another port/project/state directory.
  The installer refuses to adopt someone else's containers or volumes.
- **GPU busy:** free memory yourself, select `--gpu ID` during a new install, or
  use an external OCR endpoint. It will not shut down your other workloads.
- **OCR model missing / checksum mismatch:** use the specified snapshot in an
  intact Hugging Face cache or let a new installation download its own copy.
- **Startup failed:** configuration/data are preserved. Run `logs`, correct the
  reported problem, then `start`. For OCR-specific logs, use
  `docker logs casecite-demo-ocr-1 --tail 100` (adjust the project name if needed).
- **First-account token rejected:** use `token` with the correct `--state-dir`.
  After the first administrator is created, invite additional users in Settings.

There is deliberately no destructive reset command. See [operations](operations.md)
for backup and recovery. For a public server, configure TLS, `DEBUG=false`, the
correct `PUBLIC_URL` and your deployment-specific access policy; the installer
defaults to a localhost evaluation, not an internet-facing deployment.

## Publication and verification

The GitHub workflow publishes only after Go/database, frontend/add-in, real
GoodMem/browser and installer checks pass. It publishes a commit tag and the
`demo` channel from the migration branch, or `latest` from main, with OCI source
and revision labels, build provenance and SBOM. Publication is limited to the
canonical repository's trusted branches; fork pull requests cannot publish.

The installer test uses fresh PostgreSQL and real GoodMem with deterministic
test providers. It verifies model registration and scoped runtime configuration,
readiness, embedded add-in delivery, secret quoting, restart stability and
project isolation. These tests validate deployment behavior, not model quality.
The existing review instance supplies the separate evidence for the pinned
GPU OCR image/profile; it is not restarted by these tests.
