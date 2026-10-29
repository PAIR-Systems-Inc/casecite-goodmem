# CaseCite GoodMem

A legal workspace being ported to a **Go server, the original React interface and Word add-in, GoodMem, and PostgreSQL**. The Go application embeds both interfaces. GoodMem owns document ingestion, original files, page images and retrieval; PostgreSQL replaces Redis for application state and background work.

Derived from [Thomas Unise's CaseCite](https://github.com/thomasunise/casecite), under Apache-2.0, at commit `6605cb3474cf0c2894080af216d5fed2a81fc997`. The original uses Python/FastAPI and React/TypeScript. Git history retains that implementation.

## Implementation status

**Full feature and UI parity is the requirement, and is not yet complete.** The earlier reduced-scope implementation is preserved at `checkpoint/core-goodmem-a74e09e`; its omissions are not accepted scope for this branch.

The current port includes local identity, rotating sessions, MFA/recovery, Microsoft OIDC, device authorization, editable permissions, user administration, audit verification, matters, document folders, GoodMem storage/preview, branding and personal settings. The contract pipeline includes original deterministic extractors and clause data, grounded review, saved analyses, comparison, long drafting, reports, and tracked DOCX redlines that preserve the original package.

The original React and Word add-in source and tests are restored. Saved workspace snapshots and shared conversation storage are implemented. Their presence does not establish that every backend workflow works. Connectors, cloud pickers, research/legal tools, review tables, advanced workflows and remaining administration paths still require implementation or integration work. Live provider, Word-host and full browser verification remain open.

See the [parity scope](docs/parity/README.md), [source comparisons and verification evidence](docs/parity/evidence.md), and [architecture assessment](docs/migration.md). Known upstream defects should be fixed, with regression tests and documented differences, rather than reproduced.

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

`TEST_GOODMEM_CONFIG=/path/to/test-connection.json go test -run TestRealGoodMem -v ./internal/app` separately exercises the actual GoodMem document lifecycle. Run it only against a test space; it creates and deletes its own temporary memory.

## Limits that matter

Whole-document contract analysis uses original PDF, DOCX or text content retrieved from GoodMem, or explicitly supplied document text. Unsupported extraction fails visibly; additional formats and scanned-document coverage still need work. Contract comparison accepts two to four documents. Long drafting creates a reviewable section plan and generates sections with shared definitions and instructions. Uploads default to 50 MB.

GoodMem performs ingestion, chunking, embeddings, retrieval, and page rendering. Configure the space's embedders to suit the corpus; optional reranking uses `GOODMEM_RERANKER_ID`. CaseCite does not duplicate a vector index or implement its own OCR pipeline. Page images depend on GoodMem's support for the uploaded format.

The current CourtListener path is not yet the original full legal-research workflow. DOCX redline export preserves the original package and uses native tracked changes; other source formats require a generated DOCX. Exact quotation matches do not establish that the model's interpretation is correct. Real Word-host and visual export verification remain open.

[License](LICENSE) · [Third-party notices](NOTICE.md) · [Architecture and tradeoffs](docs/migration.md) · [Operations](docs/operations.md)
