# CaseCite GoodMem

A focused legal workspace with a **Go server, embedded React interface, GoodMem, and PostgreSQL**. No Python runtime, Redis, separate vector database, OCR service, file store, or frontend web server is required by CaseCite.

Derived from [Thomas Unise's CaseCite](https://github.com/thomasunise/casecite), under Apache-2.0. The original uses Python/FastAPI and React/TypeScript. This branch replaces the backend and narrows the interface around the core document workflows. Git history retains the original implementation.

## What works

- Accounts, expiring sessions, password changes, administrator-created users, account disabling, and audit events.
- Private libraries and shared matters with owner-controlled membership.
- Uploads stored as GoodMem memories, processing status, authenticated original downloads, text view, page-image preview, moves between matters, and deletion.
- Research over explicitly selected documents, numbered source passages, follow-up questions, optional CourtListener search, and source-only search without a language-model call.
- Contract reviews, drafting, two-document comparison, and multi-document review tables. Quoted text is verified deterministically against the supplied evidence.
- Persistent, bounded Postgres jobs with cancellation, worker leases, failure recovery, and saved results.
- Word, Markdown, JSON, and CSV exports; human approval/flagging of review cells; encrypted personal provider keys.

This is a working implementation of that scope, **not a claim of measured 90% feature or legal-answer parity**. The deliberate omissions and architectural assessment are in [the migration analysis](docs/migration.md).

## Use an existing GoodMem installation

Build tools: Go 1.26 and Node 24. The resulting executable needs neither Node nor Go installed on the runtime host.

```bash
bash scripts/build.sh
export DATABASE_URL='postgres://casecite:password@localhost:5432/casecite?sslmode=require'
export GOODMEM_BASE_URL='https://your-goodmem-host'
export GOODMEM_API_KEY='your-server-side-key'
export GOODMEM_SPACE_ID='your-dedicated-space-uuid'
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

Use a dedicated GoodMem space and a server-side credential authorized for that space. For a new space, the explicit administrative command below creates an embedder and space, saving connection state with mode `0600`. It can reuse `GOODMEM_EMBEDDER_ID`. It does not run automatically when the application starts.

```bash
# Requires an administrative GOODMEM_API_KEY on an existing server.
# Set EMBEDDING_API_KEY or OPENAI_API_KEY, or reuse GOODMEM_EMBEDDER_ID.
./bin/casecite setup-goodmem /secure/path/goodmem.json
export GOODMEM_CONFIG_FILE=/secure/path/goodmem.json
```

## Fresh shared-Postgres stack

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

The steady-state stack has three services. Postgres hosts separate `goodmem` and `casecite` databases with separate logins. The one-time `setup` container stores the GoodMem connection in a private volume. The GoodMem image is pinned to the tested v1.0.334 digest. Setup on a fresh instance uses GoodMem's initial administrative credential; provision a suitably restricted application credential before a wider rollout.

The default port is bound to localhost. For production, set `DEBUG=false`, use an HTTPS `PUBLIC_URL`, and put the app behind your existing TLS reverse proxy. GoodMem and Postgres stay on the internal network. See [operations](docs/operations.md) for backup, restart, and migration behavior.

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

A reproducible browser suite runs against **real GoodMem and Postgres**, with a deterministic local embedding/LLM fixture to avoid external API charges:

```bash
cd frontend && npm ci --ignore-scripts && npx playwright install chromium && cd ..
bash scripts/integration.sh
```

It builds the actual Docker image, boots an isolated stack, initializes GoodMem, uploads a PDF, opens its page image and text, runs cited research and reviews, exports Word, persists review decisions, and checks session/UI behavior. The test stack is removed afterward. `KEEP_TEST_STACK=true` preserves it for inspection; ports and project name are configurable in the script. The fixture provider must never be used for real legal work.

`TEST_GOODMEM_CONFIG=/path/to/test-connection.json go test -run TestRealGoodMem -v ./internal/app` separately exercises the actual GoodMem document lifecycle. Run it only against a test space; it creates and deletes its own temporary memory.

## Limits that matter

Whole-document review uses the original PDF, DOCX, or text file retrieved from GoodMem. For files the Go text readers cannot decode, it falls back to **retrieved excerpts and labels that coverage**. It never treats a missing clause in excerpts as proof the full document lacks it. Review is bounded to 12 documents, 120,000 characters per document, and 180,000 total characters; larger work should be split or handled through targeted research. Uploads default to 50 MB.

GoodMem performs ingestion, chunking, embeddings, retrieval, and page rendering. Configure the space's embedders to suit the corpus; optional reranking uses `GOODMEM_RERANKER_ID`. CaseCite does not duplicate a vector index or implement its own OCR pipeline. Page images depend on GoodMem's support for the uploaded format.

CourtListener integration provides search excerpts and links, not a full citator or exhaustive research workflow. Word export is a new, clean document; it does not preserve original Word formatting or produce tracked-change redlines. Review-table exports support CSV/JSON, not XLSX. Quotes matching source text does not establish that the model's interpretation is correct.

[License](LICENSE) · [Third-party notices](NOTICE.md) · [Architecture and tradeoffs](docs/migration.md) · [Operations](docs/operations.md)
