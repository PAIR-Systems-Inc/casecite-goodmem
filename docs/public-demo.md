# Public document demo

`PUBLIC_DEMO=true` runs a separate, restricted interface. Use a dedicated
database and GoodMem setup. Startup refuses existing application accounts or
password hashes. Normal CaseCite mode is unchanged when this flag is absent.

- Anyone can ask questions about the operator-loaded public collection.
- Public visitors cannot upload. Login, signup, administration, connectors,
  jobs, file downloads, sharing, and ordinary document APIs are not registered.
- A private invitation opens one guest's collection. Documents are never added
  to the public collection or another guest's collection. Existing GoodMem
  per-user credentials and document policies enforce retrieval access.
- This is access isolation, not end-to-end encryption. The operator and model,
  embedding, and OCR providers can process document content.
- Chat is stateless on the server. The current browser view holds questions
  and answers in memory. Nothing is saved in localStorage or shared history.

## Operator commands

Run these in the configured CaseCite container. They use the same database,
`SECRET_KEY`, and scoped `GOODMEM_CONFIG_FILE` as the web process.

```sh
casecite demo-seed /public-documents/sample-agreement.txt
casecite demo-invite safwan 168h
casecite demo-revoke safwan
```

`demo-seed` is an explicit publication operation: anyone can query these files.
Use approved or synthetic documents. It waits for indexing and uses a stable
document ID, so retrying the same file does not create another document.

`demo-invite` prints a single-use URL. Send it privately. The token is in the
URL fragment and is exchanged through POST for a Secure, HttpOnly cookie. The
database stores only SHA-256 hashes of the invitation and session credentials.
The browser removes the fragment before redemption. No application password is
created. Anyone with an unused invitation can redeem it; it is not identity
verification. Do not paste it into issue trackers or shared logs.

The duration must be 1h–720h. It limits both redemption and the resulting
session. Reissuing an invitation for the **same slug** replaces the old link and
session, preserves documents, and preserves the upload count. Signing out,
clearing cookies, or switching browsers requires a new invitation. Revocation
blocks subsequent private requests; it cannot erase files or answers already
seen. Expiry/revocation do not delete stored documents.

Each guest can upload **10 documents total**. PostgreSQL reserves a slot in the
same transaction that inserts a document. Concurrent uploads cannot bypass the
limit. Invalid files consume no slot. An accepted upload that later fails or
has an uncertain GoodMem result still consumes a slot. Deleting a document or
reissuing an invitation does not reset this count. Do not create a new slug to
renew the same person's access.

Questions are limited to 2,000 bytes, six retrieved passages, and 1,200 output
tokens. All demo users share a durable budget of 12 questions/minute and
300/24h. This bounds public provider usage; configure provider-side spending
limits as well. Do not trust arbitrary forwarded IP headers for rate limits.

`MAX_UPLOAD_MB` sets the file limit. The PairHub deployment uses 10 MiB, a
dedicated database, external model/OCR endpoints, and an immutable app image.
Service API keys, database credentials, and the encryption key are still
required. “No passwords” here means no application account passwords.

## Validation

`go test -race ./...` with `TEST_DATABASE_URL` covers invitation reuse,
revocation, expiry, concurrent upload limits, API bypasses and collection
isolation. `scripts/integration.sh` also checks public/private retrieval against
the pinned real GoodMem server. The frontend suite tests delayed private
responses and expired sessions; `e2e/public-demo.spec.ts` checks the public and
invited interfaces. No paid model calls are needed for these tests.
