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

Scans reuse complete or partially cached GoodMem OCR. The rebuild uses the
configured GoodMem space and reports its actual embedder model identifiers when
available. CaseCite's stored model preference does not reconfigure the space.
Per-user embedding/chunking controls still need their final integration; this
checkpoint does not claim that saving those preferences changes GoodMem.

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
schemas, original router/service tests, and the Advanced settings consumer.
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
