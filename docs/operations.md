# Operations

## State and secrets

Back up both application databases and keep their snapshots coordinated. CaseCite document rows reference GoodMem memory IDs; restoring only one side can leave dangling references. Also back up the stable `SECRET_KEY` and GoodMem connection file/credential. The setup JSON contains a secret and is written with mode `0600`; do not commit it or copy it into a frontend asset directory.

The fresh Compose setup creates two databases and two ordinary login roles on one PostgreSQL 17 instance with pgvector. CaseCite runs its schema creation against `DATABASE_URL`; GoodMem manages its own schema. Sharing the same instance is supported; sharing GoodMem's tables is not required. For an existing instance, create the CaseCite database/role without rerunning the fresh-volume initialization script.

The initial schema and embedded, numbered SQL migrations run under a migration lock. `cc_migrations` records checksums, and a changed previously applied migration is rejected. This upgrades the Go schema; it is not an importer for the upstream Python database.

## Jobs and recovery

Two workers per Go process claim durable jobs. At most five pending/running jobs are allowed per user. Jobs have a one-hour default budget (`JOB_TIMEOUT_SECONDS`), a 30-second lease renewed every five seconds, and explicit cancellation. Successful job completion and saved analysis/results commit together. A process crash leaves the lease to expire; another live instance's maintenance task marks the job failed. Users choose whether to retry. Model charges from an interrupted request cannot be reliably rolled back.

Completed jobs are retained for 30 days; their saved results remain until explicitly deleted. Expired KV values and expired sessions are cleaned periodically. Audit events have no automatic deletion policy in this first version. Define a retention policy before a large rollout.

Review-table runs commit one document at a time so the grid shows partial progress. `REVIEW_TABLE_CONCURRENCY` controls document concurrency (default 4, range 1–32); one table has at most one active run. Cancellation retains rows already committed and prevents subsequent writes. Table/schema edits and human cell reviews serialize with extraction writes, preserving overrides, notes and approvals. A changed model value reopens review only when there is no override. Run history follows queue completion/cancellation/lease expiry transactionally and survives the queue's 30-day cleanup.

Review-table prompt estimates use the original 30,000-character excerpt budget and field overhead. GoodMem does not expose the original application's chunk count; without a measured text length, the estimate uses the excerpt ceiling instead of inventing a chunk count. Each run still makes at most one extraction call per selected document. This is an input-token budget, not a price quote.

Uploads can report `upload_uncertain` if the browser/server connection ends around GoodMem's acceptance of the file. Refresh attempts to reconcile the preallocated memory ID. If GoodMem never received it, delete the application record and upload again. A failed processing state is explicit; the user can inspect the original, delete it, and upload a corrected copy. There is no automatic, paid re-embedding loop.

## Access and deployment

This version targets one firm/workspace per installation, with a dedicated GoodMem space. Matter members can read matter documents/results; item owners control mutation/deletion. Administrators manage accounts, but do not automatically gain access to every private document. Membership revocation is checked for future requests and saved source-dependent results; content someone already downloaded cannot be revoked.

Use `DEBUG=false` and an HTTPS `PUBLIC_URL` in production. Cookies are Secure, HttpOnly for sessions, and SameSite=Lax. Browser mutations require a signed CSRF token, and cross-origin requests are restricted. Session tokens are random and stored as hashes. Personal provider keys are encrypted with AES-GCM. Never expose the GoodMem administrative credential to the browser.

The app accepts an optional Bearer session token for scripted clients. Sessions expire after eight hours or two hours idle. Changing a password invalidates other sessions; disabling an account invalidates all sessions and cancels queued/running jobs. First-account registration requires the installation token; further accounts require an administrator.

The current rate limiter uses the direct peer IP, ignoring forwarded headers. Behind a reverse proxy, configure appropriate per-client limits at that proxy; the application limit acts as an additional shared bound. No automatic trust is granted to arbitrary `X-Forwarded-For` values.

`/health` and `/ready` check the application database and the configured GoodMem space. They return 503 when a dependency is unavailable. They do not certify embedding/model-provider availability or generation quality. Logs avoid API key values and document text; review logging changes before enabling verbose provider debug output.

## Coverage and API boundaries

Original files and page-image bytes are fetched through authenticated CaseCite routes, keeping the GoodMem key server-side. PDF/DOCX/text extraction for whole-document tasks is a lightweight Go reader, separate from ingestion. When it cannot extract usable text, the result labels its coverage as retrieved excerpts. Exact quotation matches establish only that supplied text contains that quote.

AI requests currently use an OpenAI-compatible `/chat/completions` endpoint with structured JSON for reviews and drafts. Providers must support the configured model, `max_completion_tokens`, and JSON response format. Long-draft section generation retries transient failures up to three attempts; those retries may incur provider charges. Other job retries remain explicit. Output-limit failures and invalid structured output are reported, and failed reconciliation preserves the assembled sections.

Analysis reports and drafts generate new documents. DOCX redline exports preserve the original package and add native tracked revisions to affected runs; non-DOCX sources use a generated document. Ambiguous or unavailable anchors appear in export notes with the full proposal. Actual Word-host and visual rendering verification remain open. CSV cells are escaped against spreadsheet formula execution. Model content is not trusted HTML.

## Testing

`go test -race` covers the API with a real test Postgres database and local provider fixtures. Tests use separate temporary schemas and remove them. The opt-in real GoodMem test checks PDF storage, retrieval, page images and idempotent deletion. The Docker/browser integration stack uses separate database roles and exercises the built image with real GoodMem. Its provider returns fixed test answers and deterministic vectors; it is intentionally unsuitable for semantic-quality evaluation.

After dependency changes: run `go mod tidy`, `npm ci`, regenerate `NOTICE.md` using `node scripts/notices.mjs`, then rerun Go and browser checks. Node and Go are build-time tools; the deployed application is a static Go executable plus CA certificates.
