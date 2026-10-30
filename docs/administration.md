# Index maintenance and system diagnostics

`POST /api/v1/settings/reindex` rebuilds only the caller's documents and requires
`documents.upload`. The original synchronous response is retained. The browser
uses `?async=true`, receives a durable job ID and polls `/api/v1/jobs/{id}`.
Repeated submissions attach to the same active rebuild. A disconnected browser
can reconnect by clicking Rebuild again; cancelling the job stops further work
without undoing documents already rebuilt.

Each replacement is uploaded to a new GoodMem memory, awaited, and checked for a
searchable passage before a PostgreSQL transaction changes the active index.
Provider failure, empty extraction, lost permissions, cancellation and concurrent
deletion leave the previous index in place. The transaction checks the job lease
and document revision. Originals and page images keep their existing memory IDs;
matter, folder and metadata stay on the existing document record.

Scans reuse complete or partially cached GoodMem OCR. A rebuild snapshots the
caller's document embedding selection and chunk settings when it is queued.
Changing settings while it runs does not change the remaining documents' target
configuration. Its summary reports the selected model, or the configured space's
embedder identifiers when the instance default is selected.

New memory IDs are registered for delayed cleanup before uploading. Active jobs
and active document pointers protect their memories. Cancelled, failed or expired
jobs leave cleanup records for the PostgreSQL maintenance worker; retired derived
indexes are removed after ten minutes. Original memories remain protected. This
also covers a remote upload that finishes after its caller has disconnected.

`POST /api/v1/settings/clear` requires `documents.delete` and takes an owner-only
snapshot. Each document uses the normal deletion path. The response counts
successful and failed removals, with `status: partial` if some failed and HTTP
502 if none completed. Failed records remain available for retry. Unlike the
original error message, it does not claim that no remote content was deleted
when removal of one of several remote memories may have succeeded.

## Health and monitoring

- `GET /health` returns HTTP 200 with `healthy` or `degraded`, a timestamp and
  stable process start time. Details require an administrator's valid session,
  completed password/MFA setup, or an explicitly enabled development mode.
- `GET /ready` returns 200 only when PostgreSQL and the configured GoodMem space
  are reachable; otherwise it returns 503 and generic dependency names. Both
  probes run concurrently with independent five-second limits. No paid model
  request or user API key is needed.
- `GET /metrics` requires an administrator in production and accepts the same
  session cookie or bearer token as the application. JSON reports document
  status counts, PostgreSQL job/session/cache counts, connection-pool use and Go
  process metrics. `Accept: text/plain` selects Prometheus gauges. Responses are
  not cached and contain no document contents or credentials.

Redis is absent. Jobs, sessions and expiring values are reported as PostgreSQL
resources. GoodMem health is reported directly. The pinned GoodMem memory API
does not expose an authoritative per-memory chunk count: JSON marks incomplete
chunk totals with `chunk_counts_complete: false`, rather than presenting an
estimated count as exact. Space-wide vector totals are not invented from the
number of documents.

## Source comparison and validation

The cross-check uses the pinned original settings/system routers,
`services/diagnostics.py`, document reindex/clear implementations, response
schemas, original router/service tests, and the Advanced/Embedding/Retrieval
settings consumers. The original Python embedding service reads instance-wide
model/chunk settings, while the settings route stores per-user preferences;
the Go implementation connects the per-user document choices to real indexing.
`scripts/parity/administration_fixtures.py` executes 31 original rebuild summaries
and 25 clear-index responses. Go tests also exercise owner scope, permissions,
cookie/bearer metrics authentication, dependency failures, partial deletion,
duplicate rebuild submissions, OCR reuse, cancellation, offboarding and delayed
memory cleanup against PostgreSQL and controlled GoodMem HTTP responses.

The built-image browser test rebuilds text and scanned documents through the
original settings UI against real GoodMem, checks the replacement index IDs and
unchanged original IDs, then checks JSON and Prometheus metrics. Generation and
OCR responses remain controlled local fixtures; this verifies integration, not
real-model recognition or answer quality.

GoodMem contracts were cross-checked against its source and
[memory API documentation](https://docs.goodmem.ai/docs/reference/api-reference/grpc/memory/).

## Embedding and chunk settings

Settings → Embedding lists document models from GoodMem. The configured space's
embedders are available automatically. To offer another model, configure a dense
text embedder in GoodMem and give it label `casecite=enabled`. Models belonging to
other applications are not listed merely because the server credential can read
them. The catalog returns names, model identifiers, dimensions and IDs; it never
returns credentials or endpoint URLs. CaseCite's provider policy filters choices.
Selected models must still be enabled when a new upload or rebuild starts.
The instance default is checked against the same provider policy. With provider
restrictions enabled, retrieval rechecks each space's current embedder endpoint,
including legacy documents; stored profile metadata cannot bypass the policy.

The instance default retains the configured space. Selecting another embedder
creates/reuses a private GoodMem space identified by the base space and embedder.
Concurrent creation uses a stable UUID and verifies the returned association.
The application credential needs permission to list/read enabled embedders,
execute them, create/read the corresponding spaces, and manage/retrieve memories.
No provider credentials are copied from CaseCite into GoodMem. Operators configure
those credentials in GoodMem, including keyless on-premise embedding endpoints.

Each document stores its actual index space and chunk configuration. Search,
chat, strategy and deep research query the spaces of the authorized document
revisions, including older documents and shared matters using different models.
They retain explicit memory-ID filters in every space. A failed replacement
leaves the old space/index pointer intact. Originals and OCR-derived text use the
same frozen profile during initial ingestion, even if preferences later change.

Retrieval settings specify chunk size and overlap in **characters**, with defaults
of 1,800 and 200. They are passed to GoodMem on every memory creation. GoodMem
v1.0.334's token-count splitter currently falls back to character counting, so
the UI names the real unit instead of promising token accuracy. Existing saved
numeric preferences are retained; only new uploads and explicit rebuilds apply
them. A zero overlap is supported. Changing settings does not automatically
trigger potentially expensive reindexing.

Clause comparison retains its separate OpenAI/Voyage/Cohere or local endpoint
selection and BYOK behavior, described in [providers](providers.md). Its selector
is labeled separately. The original `cohere-embed-v3` UI value is normalized to
the actual `embed-english-v3.0` model name. Dimensions come from the configured
GoodMem embedder; the document selector cannot claim an arbitrary vector size.

The space creation contract is checked against GoodMem's source and
[REST space API](https://docs.goodmem.ai/docs/reference/api-reference/rest/spaces/createSpace/).
The pinned release uses authorization grants and rejects the obsolete
`publicRead` field still shown in some REST documentation; the client omits it.
PostgreSQL/race tests cover mixed-space retrieval, disabled models, catalog
privacy, concurrent space creation, queued-setting snapshots, OCR reservations
and unchanged original bytes. The integration stack registers a second local
test embedder to exercise an actual cross-space rebuild through the browser.
