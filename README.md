# CaseCite GoodMem

A legal workspace being ported to a **Go server, the original React interface and Word add-in, GoodMem, and PostgreSQL**. The Go application embeds both interfaces. GoodMem owns document ingestion, original files, page images, OCR and retrieval; PostgreSQL replaces Redis for application state and background work.

Derived from [Thomas Unise's CaseCite](https://github.com/thomasunise/casecite), under Apache-2.0, at commit `6605cb3474cf0c2894080af216d5fed2a81fc997`. The original uses Python/FastAPI and React/TypeScript. Git history retains that implementation.

Generation supports native Claude/Gemini and OpenAI-compatible endpoints, including keyless local models. The original admin endpoint and CourtListener controls persist encrypted settings in PostgreSQL. Clause embeddings use a separate OpenAI/Voyage/Cohere or local provider; GoodMem retains document indexing and retrieval. See [provider configuration](docs/providers.md).

Search-index rebuilds run as durable PostgreSQL jobs, check replacement GoodMem indexes before switching, and reuse completed OCR. Per-user model and chunk settings reach GoodMem; older indexes remain searchable across model changes. Owner-only clearing, health/readiness and JSON/Prometheus metrics are implemented. See [administration and index settings](docs/administration.md).

Native Go readers cover Word, spreadsheets, presentations, OpenDocument, email, RTF and text formats, including ruled PDF tables and Excel 2–95 workbooks. The original extraction and PDF-preview APIs are available; scans and unsupported PDF layouts use GoodMem OCR. Its HTML tables are normalized without another model call, and the text viewer displays structured rows and columns. Older Excel files reuse the durable GoodMem text-index path without an OCR call. See [document formats, comparisons and remaining fidelity work](docs/document-formats.md).

## Implementation status

**Full feature and UI parity is the requirement, and is not yet complete.** The earlier reduced-scope implementation is preserved at `checkpoint/core-goodmem-a74e09e`; its omissions are not accepted scope for this branch.

The current port includes local identity, rotating sessions, MFA/recovery, Microsoft OIDC, device authorization, editable permissions, user administration, audit verification, matters, document folders, GoodMem storage/preview, branding and personal settings. The contract pipeline includes original deterministic extractors and clause data, grounded review, saved analyses, comparison, long drafting, reports, and tracked DOCX redlines that preserve the original package.

The original React and Word add-in source and tests are restored. Word tracked replacements verify complete passages, preserve the tracking mode and handle repeated clauses through explicit selection. See [Word add-in behavior and verification](docs/word-addin.md). Saved workspace snapshots and shared conversation storage are implemented. Tabular review includes the four original templates, custom schemas, typed extraction, source highlights, scoped reruns, human overrides/approvals, history, and cited Excel/CSV/JSON exports.

Saved workflows now include all seven original step types, the three original templates, frozen run versions, conditions, substitutions, approval gates, resume/retry/skip controls, and optional email notifications. Deep research supports editable plans, document/folder/matter filters, bounded parallel searches, verified quotes, saved/shared runs, history and cancellation. Workflow research steps use the same pipeline; citation checks use CourtListener opinion identities and citation-context treatment scans.

The eight legal-tool routes cover case search and pagination, separate opinion/cluster lookups, citation checks, precedents, docket search/details, oral arguments, and yearly trends. Shared CourtListener pacing and retries apply across tools and research; expiring trend and court-name caches use PostgreSQL.

Judge intelligence includes all eight original routes: name search, durable profile builds/refreshes, biography and career, filtered opinions/full text, statistics, all nine computed metrics with methodology, and administrator deletion. Profiles and metrics use PostgreSQL. The original screens show build progress, data coverage and partial-data warnings. Briefs and Q&A use server-held profile evidence, selected GoodMem documents, source checks and saved history. See [judge intelligence](docs/judge-intelligence.md).

Shared chat now supports scoped GoodMem retrieval, per-file answers, strategy briefs, bounded case-law research, verified quotations and support checks, transactional conversation history, cancellation, and Word/PDF/Markdown exports. The browser uses durable PostgreSQL jobs. See [chat behavior and limits](docs/chat.md).

All eight connector adapters and four file pickers are implemented in Go, including OAuth, rotating grants, encrypted instance credentials, pagination, incremental sync and GoodMem OCR imports. See [connector configuration and verification limits](docs/connectors.md). Remaining document-format fidelity still requires implementation or integration work. Migrating an existing installation's data requires a separate deployment plan. Live provider, Word-host and complete browser verification remain open.

See the [parity scope](docs/parity/README.md), [source comparisons and verification evidence](docs/parity/evidence.md), and [architecture assessment](docs/migration.md). Known upstream defects should be fixed, with regression tests and documented differences, rather than reproduced.

## Quick local evaluation — prebuilt images

```bash
git clone --branch goodmem-migration https://github.com/PAIR-Systems-Inc/casecite-goodmem.git
cd casecite-goodmem
bash scripts/casecite.sh install
```

Requires Docker/Compose and Bash; no Go, Node or local application build. The installer
prompts for provider keys, generates secrets, downloads pinned OCR weights, provisions
its own GoodMem instance and waits for readiness. Open **http://localhost:8000**.
Use `bash scripts/casecite.sh token` to retrieve the first-administrator setup token.

Local OCR needs an available NVIDIA GPU and Container Toolkit. For a machine without
one, pass `--ocr-url http://ocr-server:8000/v1`. See the **[portable quickstart](docs/quickstart.md)**
for hardware requirements, existing model caches, restart/update commands and demo preparation.

## Use an existing GoodMem installation

For a separate instance on this workstation, see [local evaluation with GPU OCR](docs/local-evaluation.md).

Build tools: Go 1.26 and Node 24. The resulting executable needs neither Node nor Go installed on the runtime host.

```bash
bash scripts/build.sh
export DATABASE_URL='postgres://casecite:password@localhost:5432/casecite?sslmode=require'
export GOODMEM_BASE_URL='https://your-goodmem-host'
# Provision separately as an administrator; see docs/goodmem-security.md.
export GOODMEM_CONFIG_FILE='/secure/runtime/goodmem.json'
export SECRET_KEY="$(openssl rand -hex 32)"
export REGISTRATION_BOOTSTRAP_TOKEN="$(openssl rand -hex 32)"
export DEBUG=true
export PUBLIC_URL=http://localhost:8000
# Optional shared model key; personal keys can be added in Settings.
export OPENAI_API_KEY='your-model-key'
./bin/casecite
```

Create a `casecite` database and a dedicated `casecite` role on the **same Postgres instance** as GoodMem. CaseCite initializes its `cc_*` tables there. It does not modify GoodMem tables or require a Redis extension. Keep `SECRET_KEY` stable and backed up: it protects stored provider credentials and CSRF signatures. The executable reads process environment variables, not `.env` automatically.

Open `http://localhost:8000`, create the first administrator using the setup token, then create additional users in Settings. There is no public signup after the first account.

GoodMem setup creates a scoped application controller and a reserve of separate reader identities. Every user's document reads and searches use their own reader identity. Document and matter grants are enforced by GoodMem as well as the Go application. The administrative credential is kept in a separate provisioning file; it is never the runtime key.

```bash
# Administrative shell only. For a fresh GoodMem server use GOODMEM_INITIALIZE=true.
export GOODMEM_API_KEY='your-administrative-key'
export GOODMEM_PROVISIONING_FILE=/secure/provisioning/admin.json
# Set EMBEDDING_API_KEY or OPENAI_API_KEY, or reuse GOODMEM_EMBEDDER_ID.
./bin/casecite setup-goodmem /secure/runtime/goodmem.json
unset GOODMEM_API_KEY GOODMEM_PROVISIONING_FILE
export GOODMEM_CONFIG_FILE=/secure/runtime/goodmem.json
```

See [GoodMem security and upgrades](docs/goodmem-security.md) before upgrading an existing library. Legacy root-key runtime configurations are rejected; `migrate-goodmem-security` copies and verifies existing documents before removing their old shared-space copies. Provisioning adds no running service or Redis dependency.

## Fresh shared-Postgres stack — build from source

For the prebuilt, automated installation, use the quickstart above. These manual
commands remain available for developers and custom deployments.

```bash
node scripts/init-env.mjs
# Edit .env: add OPENAI_API_KEY (or separate EMBEDDING_API_KEY).
# These are not needed if you reuse an existing, working embedder.
docker compose up -d postgres goodmem
docker compose logs -f goodmem
# Once the REST server is listening, exit log following with Ctrl-C:
docker compose run --build --rm setup
docker compose up --build -d app
```

Open `http://localhost:8000`. The installation token is `REGISTRATION_BOOTSTRAP_TOKEN` in your local `.env`. `init-env.mjs` refuses to overwrite an existing file and generates independent database passwords and application secrets.

The application stack has three services. Postgres hosts separate `goodmem` and `casecite` databases with separate logins. The administrative `setup` container writes scoped runtime credentials to `connection` and keeps its administrative credential in `provisioning`. Only `connection` is mounted by the application. The GoodMem image is pinned to the tested v1.0.334 digest. OCR additionally needs GoodMem's on-premise model service, configured with `GOODMEM_OCR_BASE_URL`; see [OCR setup and behavior](docs/ocr.md). Setup automatically replaces the initialization credential with scoped service credentials for runtime use.

The default port is bound to localhost. For production, set `DEBUG=false`, use an HTTPS `PUBLIC_URL`, and put the app behind your existing TLS reverse proxy. GoodMem and Postgres stay on the internal network. See [operations](docs/operations.md) for backup, restart, and migration behavior.

Optional [Google Workspace SSO](docs/google-sso.md) uses the existing account and MFA system. It requires a Google OAuth web client and an explicit domain allowlist; invitation-based enrollment is the default.

## Development and verification

```bash
cd frontend
npm ci --ignore-scripts
npm run dev                   # http://localhost:3000, proxies /api to :8000
# In another shell, with the server environment configured:
cd server
go run ./cmd/casecite
```

```bash
cd server
go vet ./...
go test ./...
# Runs database, authorization, queue, and API integration tests:
TEST_DATABASE_URL='postgres://postgres:test@localhost:5432/casecite_test?sslmode=disable' go test -race ./...
```

The original frontend and add-in unit suites are retained:

```bash
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix word-addin run typecheck
npm --prefix word-addin test
```

The integration harness uses **real GoodMem and Postgres**, with a deterministic local embedding/LLM fixture to avoid external API charges:

```bash
cd frontend && npm ci --ignore-scripts && npx playwright install chromium && cd ..
bash scripts/integration.sh
```

It builds the Docker image and tests the restored UI's sign-in, matter creation, membership display, upload/indexing, original-file and text retrieval, persistent login, Word manifest and logout. The original mocked browser suite runs separately without `E2E_BASE_URL`. Neither suite establishes full parity. The test stack is removed afterward. `KEEP_TEST_STACK=true` preserves it for inspection; ports and project name are configurable in the script. The fixture provider must never be used for real legal work.

`scripts/integration.sh` also runs `TestGoodMemSecurityLive` against the pinned GoodMem server. It tests unauthorized reads and retrieval without application filters, OCR and page-image boundaries, membership changes, retirement, outage recovery, and the legacy-library upgrade. Its administrative credentials and database volumes are disposable test fixtures.

## Limits that matter

Whole-document contract analysis uses original PDF, DOCX or text content retrieved from GoodMem, or explicitly supplied document text. Scanned/mixed PDFs use GoodMem OCR with page coverage, PostgreSQL caching and durable indexing of the extracted text back into GoodMem. Missing pages block full-document analysis. Other binary formats can use GoodMem's completed page images; full format parity remains unverified. Contract comparison accepts two to four documents. Long drafting creates a reviewable section plan and generates sections with shared definitions and instructions. Uploads default to 50 MB.

GoodMem performs ingestion, chunking, embeddings, retrieval, and page rendering. Configure the space's embedders to suit the corpus; optional reranking uses `GOODMEM_RERANKER_ID`. CaseCite does not duplicate a vector index or implement its own OCR pipeline. Page images depend on GoodMem's support for the uploaded format.

CourtListener tools, judge profiles/analytics, judge briefs and Q&A, and deep research use the shared Go chat and job workflows. DOCX redline export preserves the original package and uses native tracked changes; other source formats require a generated DOCX. Exact quotation matches do not establish that the model's interpretation is correct. Real Word-host and visual export verification remain open.

[License](LICENSE) · [Third-party notices](NOTICE.md) · [Architecture and tradeoffs](docs/migration.md) · [Operations](docs/operations.md)
