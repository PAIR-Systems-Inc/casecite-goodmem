# Verification record

This records the initial Go/GoodMem core checkpoint, before the full upstream
UI and feature port. For the current branch's tests and remaining work, see
[the parity evidence](parity/evidence.md). Binary size and dependency counts
below describe that earlier checkpoint.

Implementation checked on 2026-10-29. All document fixtures and accounts were synthetic.

| Check | Result |
|---|---|
| Go 1.26.10 `go vet ./...` | Passed |
| `go test -race ./...`, real PostgreSQL 17 | Passed |
| Real GoodMem v1.0.334 document lifecycle | Passed: PDF upload, completion polling, byte-identical original download, page metadata, image download, filtered retrieval, delete and delete retry |
| API integration | Passed: first-account setup, invite/password change, sessions, CSRF rejection, private/matter isolation, revocation including follow-ups, scoped retrieval, review/draft/comparison jobs, verified/unverified quotations, review decisions, exports, atomic nonces, queue bounds and cancellation |
| React TypeScript and Vite build | Passed |
| Clean Docker stack and browser workflow | Passed with separate Postgres roles/databases, real GoodMem and deterministic local model/embedding fixtures |
| Desktop/mobile screenshots | Inspected at 1280px and 390px; no layout overflow observed |
| `npm audit` | No reported vulnerabilities in the installed 83-package dependency graph |
| Go vulnerability scan | No vulnerabilities in imported packages or reachable code; one advisory exists elsewhere in a required module, outside the imported packages |
| Standalone binary | Built with `CGO_ENABLED=0`, embedded frontend; approximately 13 MB for Linux amd64 |

The browser run covers installation setup, matter creation, PDF upload and processing, image/text preview, cited research, contract review, Word download, review-table approval persistence, saved-work history, settings, logout/login, and mobile layout. Drafting and two-document comparison also run through the API integration suite.

This does **not** measure legal answer quality, retrieval recall, original feature-parity percentage, load capacity, live CourtListener behavior, or live paid-provider interoperability. The browser provider returns fixed test answers and deterministic vectors; it verifies the application/GoodMem contract, not model semantics. Production-quality evaluation needs a representative legal corpus and the intended embedding, reranking, and generation models.
