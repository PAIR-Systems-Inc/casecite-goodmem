# On-premise OCR through GoodMem

CaseCite calls GoodMem's `POST /v1/ocr:document` API. It does not run
Tesseract, a Python OCR process, or a hosted OCR fallback. Original files and
page images remain in GoodMem. PostgreSQL stores the derived text cache,
page offsets, job state and short renewable leases.

## Configure the GoodMem OCR add-on

Run the GoodMem OCR add-on/model service on your own infrastructure, and set
`GOODMEM_OCR_BASE_URL` **on the GoodMem server** to its OpenAI-compatible
dots.ocr endpoint. The endpoint base includes `/v1`; GoodMem appends
`/chat/completions`. CaseCite's GoodMem credential needs `OCR_DOCUMENT`
permission as well as its existing memory/space permissions.

The bundled Compose configuration explicitly sets:

```dotenv
GOODMEM_OCR_BASE_URL=http://host.docker.internal:8001/v1
```

This addresses a model service on the Docker host. Change the hostname/port for
your installation; the service must be reachable from GoodMem's container.
Compose supplies the Linux host-gateway mapping. Apply a changed setting by
recreating the GoodMem service. If you use an existing GoodMem installation,
configure that installation; setting this variable only on the Go process has
no effect.

The GoodMem base image does not include the OCR model/runtime. Its default OCR
endpoint is remote in the referenced source, so do not rely on that default for
an on-premise installation. The Compose override above prevents that implicit
remote selection. A missing local model produces a visible error.

The application stack still comprises Go, GoodMem and PostgreSQL; the OCR model
is an additional runtime dependency, which can be part of an existing GoodMem
deployment. This change does not automatically make embedding and generation
local: configure those endpoints separately if they must also stay on premises.

## Scan ingestion and full-document reading

The tested GoodMem v1.0.334 image offers the OCR API, but normal ingestion does
not automatically OCR an image-only PDF. Such a memory retains its original
and can produce page images while reporting text ingestion as failed.

CaseCite detects sparse and image-bearing PDF pages during upload and queues a
durable OCR indexing job. The worker reads the original from GoodMem, OCRs the
needed pages through GoodMem, and creates a second GoodMem memory containing
the complete assembled text. GoodMem chunks and embeds that text. The document
becomes searchable only after this derived memory finishes indexing. Search
uses the derived memory and maps every hit back to the original document ID;
download and page-preview routes continue using the original memory.

This also covers mixed PDFs whose searchable headings would otherwise allow
incomplete native text to be indexed. Pages with usable native text and no
images use the Go reader. Sparse pages retain the original application's
50-character threshold. Image-bearing pages are handled conservatively even
when they have longer text, which can increase OCR work for pages with logos.

Full-document contracts, workflow steps, tabular reviews and source previews
share this reader. PDF OCR requests cover consecutive missing ranges of at
most ten pages. Other formats can use GoodMem's completed page-image output
when native extraction is unavailable. The OCR client supports GoodMem's PDF,
TIFF, PNG, JPEG and BMP inputs; this does not expand the UI's upload-format
allowlist. Unsupported rendering formats fail explicitly.

Layout-cell text supplies the reading order; Markdown is a fallback. Page
boundaries use two newlines. Quote and page offsets are UTF-16 units, matching
the browser and Word client. Source panels identify OCR-derived text and its
page. Quote verification checks the extracted text; it does not prove that the
OCR transcription or legal interpretation is correct.

## Failures, cost and retries

- A missing, failed, duplicate or out-of-range OCR page cannot silently become
  a complete document. Full-document analysis rejects incomplete coverage.
- Successful ranges are cached immediately. Explicit retries retain those
  pages; full-document readers reuse the same cache. Cache identity includes
  the original memory ID, reader version and content hash for partial retries.
- A PostgreSQL lease coalesces concurrent readers across Go instances. Two OCR
  operations can run per Go process; no database connection is held during OCR.
- CaseCite does not automatically retry the non-idempotent OCR request. A
  failed indexing job is retried by its owner from the Knowledge Base, or via
  `POST /api/v1/documents/{id}/retry-ocr`. Repeated clicks reuse an active job.
  Completed OCR is reused if only the subsequent embedding step failed.
- OCR requests have a ten-minute limit; durable jobs use the configured
  `JOB_TIMEOUT_SECONDS` budget. The ordinary 4 MB document-text cap still
  applies. GoodMem separately enforces its page, pixel and request limits.
- Cancellation, job lease expiry and deletion cannot publish a partial index.
  Deletion removes both memories; PostgreSQL retains delayed cleanup work to
  catch a remote create finishing after cancellation, including account removal.

Upload indexing shares the existing worker pool and does not consume the
five-job interactive analysis quota. OCR models and embedding models still
consume compute; caching reduces repeated work without claiming free OCR.

## Verification

`scripts/parity/ocr_fixtures.py` generates the checked-in scan and mixed-page
PDFs using development-only Pillow/ReportLab. Poppler confirms the scan has a
raster image and no text layer; rendered fixture pages were inspected.

The Go suite checks API encoding, page ordering/coverage, UTF-16 spans, errors,
mixed-page detection, caching/coalescing, partial retries, cancellation,
revision and membership changes, durable indexing, search identity, account
purge and deletion during a remote create. The browser suite uses the built Go
image and real GoodMem with an isolated dots.ocr protocol fixture. Its fixed
transcription verifies rendering, API integration and citation plumbing; it
does **not** establish recognition quality for the intended on-premise model.

Cross-checks: upstream CaseCite `services/text_extraction.py`, especially
`_needs_ocr`, `_extract_pdf_pdfplumber`, `_pdf_result` and `_OcrSession`; GoodMem
`proto/goodmem/v1/ocr.proto`, REST OCR DTOs, `OcrServiceImpl`, and its OCR
quickstart/limits documentation. Reproducing upstream's silent partial-text
fallback is intentionally avoided.
