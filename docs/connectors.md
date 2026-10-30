# Document connectors and file pickers

The Go server implements the eight original connector families and four browser
pickers. Imports use the same validation, original-file storage, page images,
GoodMem OCR and searchable text pipeline as local uploads. GoodMem performs
embedding using the configured space's embedder; Python and Redis are not part
of this path. This is an implementation checkpoint, not certification against
live customer accounts.

## Configuration

The environment names are in `.env.example` and forwarded by Compose. Google,
Microsoft, Box and Dropbox credentials can also be saved in Settings → Sources.
Instance values are encrypted in PostgreSQL and take precedence over environment
values. Saving one field preserves the others; clearing the instance settings
restores deployment defaults. The API accepts a blank field to clear just that
override. Client secrets and connector grants are never returned in status APIs.

| Provider | Server credentials | Additional configuration |
|---|---|---|
| Google Drive | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Picker also needs `GOOGLE_PICKER_API_KEY`; optionally `GOOGLE_APP_ID` |
| OneDrive / SharePoint | `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET` | `MICROSOFT_TENANT_ID`, default `common` |
| Box | `BOX_CLIENT_ID`, `BOX_CLIENT_SECRET` | Browser picker uses a downscoped user token |
| Dropbox | `DROPBOX_APP_KEY`, `DROPBOX_APP_SECRET` | Chooser uses the public app key |
| Clio Manage | `CLIO_CLIENT_ID`, `CLIO_CLIENT_SECRET` | `CLIO_BASE_URL` selects US/EU/CA/AU region |
| Filevine | `FILEVINE_API_KEY`, `FILEVINE_API_SECRET` | `FILEVINE_ENABLED=true`; firm-wide key restricted to administrators |
| NetDocuments | `NETDOCUMENTS_CLIENT_ID`, `NETDOCUMENTS_CLIENT_SECRET` | Regional `NETDOCUMENTS_VAULT_HOST` and `NETDOCUMENTS_API_HOST`; explicit experimental opt-in |
| iManage | `IMANAGE_CLIENT_ID`, `IMANAGE_CLIENT_SECRET` | `IMANAGE_BASE_URL` supports an on-premise deployment; explicit experimental opt-in |

NetDocuments and iManage retain upstream's `EXPERIMENTAL_CONNECTORS=true` gate.
Their adapters are implemented and tested with protocol fixtures, but have not
been validated against live tenants. Filevine requires an explicit connection
by the administrator; merely setting its firm key does not connect every user.

Register `PUBLIC_URL/api/v1/connectors/{provider}/callback` with each OAuth app.
Provider IDs are `google_drive`, `onedrive`, `box`, `dropbox`, `clio`,
`netdocuments`, and `imanage`. The corresponding `*_REDIRECT_URI` environment
variables override this default. Microsoft document access deliberately uses
`MICROSOFT_CONNECTOR_REDIRECT_URI`, separate from SSO's
`MICROSOFT_REDIRECT_URI`. A Google Picker developer key is browser-visible: use
the separately named `GOOGLE_PICKER_API_KEY`, with the provider's origin/API
restrictions. `GOOGLE_API_KEY` remains the private Gemini generation key.

## Import behavior

Knowledge Base → Sources provides individual file pickers and background sync
connections. A connected provider does not disable its picker. Background sync
requires either a folder ID through the API or explicit whole-account consent;
Clio retains the original full-account scope. Sync is started on demand, with
incremental skipping of unchanged indexed files. No recurring scheduler or
provider webhook is implied by “background.”

Original sync, callback, status and credential endpoints are preserved. OAuth
start returns a same-origin launch URL, adding
`GET /connectors/{provider}/authorize`. It resolves the configured provider
destination using a ten-minute, single-use, browser-bound state. This supports
regional and private installations without teaching the browser an unbounded
redirect allowlist. Credentials are scoped to the user and encrypted. Refresh
uses a PostgreSQL row lock, preserving rotated refresh tokens across replicas.
Disconnect cancels outstanding syncs and invalidates late callbacks. Google,
Box and Dropbox provider revocation is attempted; other adapters clear local
grants, as upstream did. Imported documents remain available after disconnect.
Account deletion also attempts provider revocation and removes local grants.

Sync traverses provider pagination and folders with cycle detection. An
unreadable child folder is reported while other folders continue; an unreadable
starting scope fails. Ten consecutive file failures stop a sync. Responses retain
`docs_processed`, `docs_skipped`, `docs_total`, `errors_count`, and the first
twenty file errors. New file content creates a new document revision so existing
citations retain their original bytes; repeated identical content is idempotent.

The four picker import endpoints preserve the synchronous response and add
`?async=true`. The UI uses durable jobs so OCR can finish beyond a browser request
timeout. Successful results contain the actual `document_ids`; sidebar chips
no longer guess from the newest documents. One file failure does not hide other
successful imports. Picker tokens and signed links are encrypted in pending job
inputs and removed when the job completes, fails, expires, or is cancelled.
Signed Dropbox links are hashed for provenance rather than stored verbatim.

Downloads enforce declared and streamed byte limits, bounded retries and
redirects. Browser-supplied URLs require HTTPS and provider hosts; DNS results
are checked before connecting, including on redirects. Cross-origin redirects
drop provider authorization/session headers. An administrator-configured
on-premise API is trusted only on its configured origin. Two OCR worker slots
are separate from two general worker slots inside the same binary, allowing
imports to wait for OCR without occupying all workers that can perform it.

## Evidence and deliberate improvements

`connector_fixtures.py` executes pinned Python schemas and provider methods:
230 request cases and 25 OAuth/listing comparisons, with source hashes. The
PostgreSQL/HTTP tests exercise all eight provider downloads, all listing adapters,
all four picker imports, refresh serialization, encrypted storage, partial
settings updates, downscoped Box tokens, browser binding, replay rejection,
disconnect during callback, cancellation during GoodMem upload, pagination,
redirect credential stripping, size limits, retries and idempotence.

The browser suite uses a simulated Clio consent page and a local Clio protocol
server. The Go API, database, GoodMem file rendering, OCR orchestration and
indexing are real. OCR transcription is controlled fixture output; this does
not measure model recognition accuracy or demonstrate live cloud-provider login.

Compared with upstream, SharePoint discovery follows all site and drive pages,
crawl cycles cannot loop forever, missing dates remain unknown instead of being
replaced with “now,” and partial discovery is disclosed. Google native files
export to Office formats and shared-drive flags are retained. Clio downloads
use the documented `.json` route; its actual filename is used when a display
title omits the extension. Signed Microsoft download URLs receive no bearer
token. The UI's content security policy permits the required picker SDKs, and
successful sync refreshes the Knowledge Base automatically.

Primary protocol references checked during implementation:

- [Google Drive listing](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/list) and [download/export](https://developers.google.com/workspace/drive/api/guides/manage-downloads).
- [Microsoft Graph children](https://learn.microsoft.com/en-us/graph/api/driveitem-list-children?view=graph-rest-1.0) and [content redirects](https://learn.microsoft.com/en-us/graph/api/driveitem-get-content?view=graph-rest-1.0).
- [Box downscoped tokens](https://developer.box.com/guides/authentication/tokens/downscope/).
- [Dropbox OAuth](https://www.dropbox.com/developers/reference/oauth-guide).
- [Clio Manage API](https://docs.developers.clio.com/clio-manage/api-reference/) and [authorization](https://docs.developers.clio.com/api-docs/clio-manage/authorization/).

Filevine's detailed portal required sign-in during this review. Its adapter,
and the gated NetDocuments/iManage adapters, were cross-checked against the
pinned original source and fixture responses; live deployment verification
remains necessary. Supported document formats and OCR limits are described in
[OCR](ocr.md) and [operations](operations.md).
