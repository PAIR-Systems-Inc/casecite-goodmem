# Contributing

Use Go 1.26 and Node 24. Keep business behavior in `server/internal/app`, frontend API access in `frontend/src/goodmem/api.ts`, and page/component rendering in the adjacent TypeScript modules. Preserve the GoodMem boundary: do not introduce a second vector index, OCR pipeline, or original-file store.

Run `gofmt`, `go vet ./...`, the Go suite with `TEST_DATABASE_URL`, the frontend build, and `scripts/integration.sh` for API/UI changes. Add tests for permission boundaries, recovery behavior and meaningful workflows; avoid tests that merely repeat implementation details. Keep all fixtures synthetic. Update notices when shipped dependencies change.

The original repository is retained as the `upstream` remote. This fork intentionally narrows its feature scope; describe product tradeoffs explicitly in a change rather than leaving disconnected UI controls.
