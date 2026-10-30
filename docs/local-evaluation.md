# Local evaluation

**On a new machine, use the [portable installer](quickstart.md).** It downloads
the prebuilt application and OCR weights and handles provisioning/readiness.
This document describes the existing workstation review project, which remains
independent of that installer.

The local review instance uses its own `casecite-review` Compose project, network,
PostgreSQL volume and GoodMem credential volumes. It does not reuse or modify the
existing GoodMem installation. The app is available at **http://localhost:18000**;
the separate GoodMem REST API/console is bound to **127.0.0.1:18080**, and its
local GPU OCR service to **127.0.0.1:18081**. Neither PostgreSQL nor GoodMem's
gRPC port is published. GoodMem and CaseCite share this project's PostgreSQL
server, with separate databases and roles.

Private configuration lives in `.local-eval/.env`, with mode 0600. This directory
is excluded from Git and Docker build contexts. The application receives only
scoped runtime GoodMem credentials; administrative setup state stays in the
separate provisioning volume. Preserve the generated `SECRET_KEY` and volumes
across restarts.

```bash
# Inspect the isolated instance.
bash scripts/local-eval.sh ps
bash scripts/local-eval.sh logs --tail 80 app goodmem
bash scripts/local-eval.sh logs --tail 40 ocr

# Stop and restart it without removing documents or accounts.
bash scripts/local-eval.sh stop
bash scripts/local-eval.sh start

# Rebuild the app after code changes, leaving GoodMem and PostgreSQL running.
bash scripts/local-eval.sh up --build -d --no-deps app
```

The launcher always specifies the project name, private environment file, and
local port override. Avoid `down -v` unless intentionally deleting this review
instance's data. The existing installation's containers and volumes are outside
this project.

For a fresh local evaluation, create `.local-eval/.env` using `.env.example` with
independent generated secrets, `PORT=18000`, `PUBLIC_URL=http://localhost:18000`,
and `BIND_ADDRESS=127.0.0.1`. Set `CASECITE_OCR_MODEL_DIR` to the cached Hugging
Face `models--rednote-hilab--dots.ocr` directory containing snapshot
`c0111ce6bc07803dbc267932ffef0ae3a51dc951` and its `blobs` directory. The NVIDIA
container runtime must be installed. Configure the embedding and generation
providers, start `postgres goodmem ocr`, wait for GoodMem's REST API and OCR
health, run `run --rm setup`, then start `app`. `DEBUG=true` enables local HTTP
cookies and the restricted
development login; use the generated administrator account for full review.
This is a local evaluation configuration, not the public HTTPS deployment.

The local override runs the cached Dots OCR model in a pinned vLLM 0.11.0
container. GoodMem calls it over the private Compose network. Model files are
mounted read-only, downloads are disabled, and the existing GoodMem installation
and model cache are not modified. The 8 GB RTX 4060 profile uses one page at a
time, 3 GB of CPU weight offloading, a 68% GPU memory budget and a 16,384-token
context. Encoder input is capped at 3.2 million pixels (about 185 dpi on A4).
This limit does not resize downloadable originals or GoodMem's stored page images. This
is a workstation evaluation profile: dense small print and large batches need
separate quality and throughput checks. To release the GPU while keeping the
workspace available, use `stop ocr`; use `start ocr` before processing scans.

The configured embedding model is OpenAI `text-embedding-3-small`, generation
uses OpenAI `gpt-5.4-mini`, and retrieval uses Voyage `rerank-3-lite`. An optional
Voyage `voyage-4-large` embedder is also registered. OCR is local; embedding,
reranking and generation send their inputs to those providers and can incur
their normal charges. Provider and connector credentials are deployment-specific.
Sign-in with Google requires its own OAuth client setup.

On this workstation, the one-page image-only notice fixture was recognized
correctly in approximately 45 seconds, with about 5.3 GB of GPU memory used by
the OCR service (about 7.4 GB including the desktop). Its OCR text was indexed
and cited in a generated answer. The original PDF download was byte-for-byte
identical, and its stored 200 dpi PNG page was retrieved through the app API.
These are smoke checks, not a dense-document accuracy or throughput benchmark.

The prepared review administrator's sign-in details are in `.local-eval/access.txt`
(mode 0600). Keep this file private. No provider keys or administrator credentials
belong in Git, screenshots, issue descriptions or build contexts.

The interface is branded **CaseCite by PairSys**. The PAIR pear mark is sourced
from the PairSys website's `src/assets/pear.svg`; the original CaseCite wordmark,
custom firm branding and upstream attribution remain available. The stacked
signature adapts to the collapsed navigation and mobile drawer.
