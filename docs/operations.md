# Operations

## State and secrets

Back up both application databases and keep their snapshots coordinated. CaseCite document rows reference GoodMem memory IDs; restoring only one side can leave dangling references. Also back up the stable `SECRET_KEY` and GoodMem connection file/credential. The setup JSON contains a secret and is written with mode `0600`; do not commit it or copy it into a frontend asset directory.

The fresh Compose setup creates two databases and two ordinary login roles on one PostgreSQL 17 instance with pgvector. CaseCite runs its schema creation against `DATABASE_URL`; GoodMem manages its own schema. Sharing the same instance is supported; sharing GoodMem's tables is not required. For an existing instance, create the CaseCite database/role without rerunning the fresh-volume initialization script.

The initial schema and embedded, numbered SQL migrations run under a migration lock. `cc_migrations` records checksums, and a changed previously applied migration is rejected. This upgrades the Go schema; it is not an importer for the upstream Python database.

## Development login

The original `POST /api/v1/auth/demo/login` is available with `DEBUG=true`.
It accepts `dev@localhost` and password `demo` (the password may be omitted).
It creates a reserved viewer in PostgreSQL so normal sessions, resource ownership
and foreign keys apply. CSRF and the shared sign-in rate limit still apply.
The development account does not prevent first-administrator registration.

`DEBUG=false` rejects both new demo logins and existing demo access/refresh
tokens. The endpoint cannot claim an existing email, reactivate a disabled user,
or sign in after the reserved identity is promoted or given a normal password.
Custom viewer permissions can remove its access but cannot elevate it beyond the
original viewer defaults. Ordinary accounts continue using local passwords,
Microsoft SSO and the configured MFA policy.

## Jobs and recovery

Two workers per Go process claim durable jobs. At most five pending/running jobs are allowed per user. Jobs have a one-hour default budget (`JOB_TIMEOUT_SECONDS`), a 30-second lease renewed every five seconds, and explicit cancellation. Successful job completion and saved analysis/results commit together. A process crash leaves the lease to expire; another live instance's maintenance task marks the job failed. Users choose whether to retry. Model charges from an interrupted request cannot be reliably rolled back.

Judge profile builds use the same queue. The UI polls progress; synchronous API callers wait on the durable job and can reconnect after disconnecting. Public judge snapshots are shared among users with `judge_intel.use`, while job status remains owner-scoped. A refresh preserves the previous complete snapshot; deletion cancels active work. Builds retrieve up to 300 linked cases, full text for the 25 most cited, 100 assigned dockets and 100 citation relationships from up to 50 authored opinions. Coverage and missing-data warnings accompany profiles. Metrics cache for 24 hours; lazy text retrieval invalidates that cache. `WIKIPEDIA_API_URL` defaults to Wikipedia's public API; it receives no CourtListener credentials.

Completed jobs are retained for 30 days; their saved results remain until explicitly deleted. Expired KV values and expired sessions are cleaned periodically. Audit events have no automatic deletion policy in this first version. Define a retention policy before a large rollout.

Review-table runs commit one document at a time so the grid shows partial progress. `REVIEW_TABLE_CONCURRENCY` controls document concurrency (default 4, range 1–32); one table has at most one active run. Cancellation retains rows already committed and prevents subsequent writes. Table/schema edits and human cell reviews serialize with extraction writes, preserving overrides, notes and approvals. A changed model value reopens review only when there is no override. Run history follows queue completion/cancellation/lease expiry transactionally and survives the queue's 30-day cleanup.

Review-table prompt estimates use the original 30,000-character excerpt budget and field overhead. GoodMem does not expose the original application's chunk count; without a measured text length, the estimate uses the excerpt ceiling instead of inventing a chunk count. Each run still makes at most one extraction call per selected document. This is an input-token budget, not a price quote.

Workflow runs keep frozen definitions and input IDs. Each completed step commits its output and any generated analyses, research runs or review table together, under the active job lease. A failed or cancelled step publishes none of its staged work products. Resume preserves completed steps and explicit skip decisions; retry intentionally resets the chosen step and all later steps. Runs waiting for approval consume no worker. Decisions recheck access and credentials before atomically recording the decision and queuing continuation. Workflow/run history survives queue retention.

Workflow limits default to 12 steps, 25 analyzed documents per step, 10 citation checks per step, and 524,288 bytes of stored output per step. Configure `WORKFLOW_MAX_STEPS`, `WORKFLOW_MAX_DOCUMENTS_PER_STEP`, `WORKFLOW_MAX_CITATIONS_PER_STEP`, and `WORKFLOW_MAX_OUTPUT_BYTES`. Extraction uses `REVIEW_TABLE_CONCURRENCY`. Truncated outputs identify removed fields and retain their summary and generated item IDs.

Research steps default to eight sub-questions, 24 logical tool calls, 60 passages, three concurrent sub-questions and one follow-up search. The corresponding `DEEP_RESEARCH_*` settings are in `.env.example`. A logical case-law search can make several HTTP requests to fetch opinions; the tool-call cap is not an HTTP-request or dollar cap. Search uses `COURTLISTENER_BASE_URL`; treatment checks require an instance or personal CourtListener token. The result records scope, tool failures, invalid references and quote verification; it does not imply exhaustive coverage or an editorial citator's conclusion.

All CourtListener consumers share a per-process gate: at most three active requests, with starts spaced by 250 ms. HTTP 429 retries at most three times; a server-wide penalty honors numeric or HTTP-date `Retry-After`, capped at 60 seconds. Other transient network/5xx failures retry once. The HTTP client refuses redirects. Pagination accepts only the configured API origin and search path; provider resource links are resolved to IDs on that fixed origin before sending credentials. Explicit case `kind=opinion` or `kind=cluster` never falls back to the other namespace; bare IDs retain the historical cluster-first lookup.

Legal trends use at most three concurrent year counts and a 55-second sweep budget, newest years first. Unavailable years are reported separately; current and future years do not determine the direction. Complete sweeps are cached in PostgreSQL for 15 minutes, scoped to the user, endpoint, credential fingerprint, topic and years. Partial sweeps are not cached. Successful court-name lookups are cached for 24 hours. These caches need no Redis service and survive Go process restarts.

Standalone deep research uses the same engine. Planning does not search documents or CourtListener. Starting a run freezes the approved plan and resolves its document selection; an unspecified selection searches the caller's currently readable documents. Explicit picks and folders form a union, with source/type filters applied to folder matches. Explicit empty picks stay empty; inaccessible selections contribute nothing. The run and queue entry are created together. Cancellation and lease expiry update saved history in the queue transaction, and deleting an active run cancels its job. Actual document access is rechecked after synthesis, including all-readable scopes. Saved runs are owner-readable or matter-shared; only owners can delete them.

Set `WORKFLOW_NOTIFICATIONS=true` and configure SMTP to enable completion, failure, approval-request and rejection emails. Each user can opt out in personal settings. Transition snapshots and an encrypted outbox keep mail retry separate from workflow execution; recipients must still have access when notifications are queued. Delivery failure never reverses an approval. No SMTP delivery is attempted when notifications are disabled.

Uploads can report `upload_uncertain` if the browser/server connection ends around GoodMem's acceptance of the file. Refresh attempts to reconcile the preallocated memory ID. If GoodMem never received it, delete the application record and upload again. A failed processing state is explicit; the user can inspect the original, delete it, and upload a corrected copy. There is no automatic, paid re-embedding loop.

## Access and deployment

This version targets one firm/workspace per installation, with a dedicated GoodMem space. Matter members can read matter documents/results; item owners control mutation/deletion. Administrators manage accounts, but do not automatically gain access to every private document. Membership revocation is checked for future requests and saved source-dependent results; content someone already downloaded cannot be revoked.

Use `DEBUG=false` and an HTTPS `PUBLIC_URL` in production. Cookies are Secure, HttpOnly for sessions, and SameSite=Lax. Browser mutations require a signed CSRF token, and cross-origin requests are restricted. Session tokens are random and stored as hashes. Personal provider keys are encrypted with AES-GCM. Never expose the GoodMem administrative credential to the browser.

The app accepts an optional Bearer session token for scripted clients. Sessions expire after eight hours or two hours idle. Changing a password invalidates other sessions; disabling an account invalidates all sessions and cancels queued/running jobs. First-account registration requires the installation token; further accounts require an administrator.

The current rate limiter uses the direct peer IP, ignoring forwarded headers. Behind a reverse proxy, configure appropriate per-client limits at that proxy; the application limit acts as an additional shared bound. No automatic trust is granted to arbitrary `X-Forwarded-For` values.

`/health` and `/ready` check the application database and the configured GoodMem space. They return 503 when a dependency is unavailable. They do not certify embedding/model-provider availability or generation quality. Logs avoid API key values and document text; review logging changes before enabling verbose provider debug output.

## Coverage and API boundaries

Original files and page-image bytes are fetched through authenticated CaseCite routes, keeping the GoodMem key server-side. Native PDF/DOCX/text readers share a GoodMem OCR fallback. Scanned/mixed PDFs receive a durable OCR indexing job; GoodMem stores and searches the extracted text while retaining the original and its page images. Missing pages block full-document analysis. PostgreSQL caches successful OCR pages and coalesces readers. See [on-premise OCR](ocr.md) for the required model endpoint, permissions, retry behavior, limits and cleanup. Exact quotation matches establish only that the extracted text contains that quote.

AI requests support OpenAI-compatible endpoints, native Claude and native Gemini. Shared utility calls retain the chosen provider. Instance endpoint and CourtListener settings persist encrypted in PostgreSQL, and optional provider policies apply before outgoing AI calls. Clause embeddings have a separate endpoint and support OpenAI, Voyage and Cohere. See [provider configuration and verification](providers.md). Transient failures share one bounded retry budget, including long drafting; other job retries remain explicit. Output-limit failures and invalid structured output are reported, and failed reconciliation preserves the assembled sections.

Analysis reports and drafts generate new documents. DOCX redline exports preserve the original package and add native tracked revisions to affected runs; non-DOCX sources use a generated document. Ambiguous or unavailable anchors appear in export notes with the full proposal. Sample contract reports, tracked variants and conversation exports have been rendered and inspected; live Microsoft Word-host verification remains open. See [Word editing behavior and verification](word-addin.md). CSV cells are escaped against spreadsheet formula execution. Model content is not trusted HTML.

## Testing

`go test -race` covers the API with a real test Postgres database and local provider fixtures. Tests use separate temporary schemas and remove them. The opt-in real GoodMem test checks PDF storage, retrieval, page images and idempotent deletion. The Docker/browser integration stack uses separate database roles and exercises the built image with real GoodMem. Its provider returns fixed test answers and deterministic vectors; it is intentionally unsuitable for semantic-quality evaluation.

After dependency changes: run `go mod tidy`, `npm ci`, regenerate `NOTICE.md` using `node scripts/notices.mjs`, then rerun Go and browser checks. Node and Go are build-time tools; the deployed application is a static Go executable plus CA certificates.
