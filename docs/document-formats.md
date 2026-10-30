# Document extraction and previews

The Go server reads full document text for analysis, comparisons, citations and
exports. GoodMem keeps the original bytes, page images and search index. Native
readers do not add a second document store or an office-conversion service.

| Format | Go extraction |
| --- | --- |
| PDF | Native text where readable; GoodMem's on-premise OCR for scanned or mixed pages |
| DOCX | Canonical visible text for exact redline anchors; interleaved paragraphs, headings and tables for upload extraction and PDF previews |
| DOC | Word 97–2003 OLE piece tables, including mixed Windows-1252 and UTF-16 runs; RTF/DOCX files saved with a `.doc` name are recognized |
| XLSX | All worksheets, cached formula values and formatted cell values |
| XLS | Excel 97–2003 BIFF8 worksheets, shared strings and typed values |
| PPTX | Presentation order, grouped shape text, tables and speaker notes; slide boundaries remain available for citations |
| ODT | Paragraphs, headings, inline text, explicit spaces, tabs and line breaks |
| CSV / TSV | Quoted cells, variable row widths and Markdown tables |
| HTML | Visible text, entities, paragraph boundaries and inline whitespace |
| EML | Decoded headers, MIME bodies, preferred plain-text alternatives and attachment names |
| MSG | Outlook property streams, message headers/date/body and attachment names |
| RTF | Scoped groups, code pages, Unicode/surrogate pairs, table separators and displayed field results |
| TXT / Markdown / JSON | UTF-8, BOM-marked UTF-16, Windows-1252 and Latin-1 |

`POST /api/v1/legal-docs/extract-text` accepts multipart field `file` and returns
the original response fields: text, character/word/page/table counts, OCR use,
truncation and warnings. `POST /api/v1/legal-docs/convert-to-pdf` passes PDFs
through unchanged and renders extracted text using embedded fonts. Both require
either `documents.view` or `contracts.use`; account password/MFA gates apply.
`GET /api/v1/legal-docs/courts` returns the original 41 jurisdiction entries to
authenticated users.

Temporary extraction calls the GoodMem OCR API directly when necessary, without
creating permanent documents or search memories. It shares the two-operation
OCR limit with stored-document reads. Native pages are retained, missing ranges
are requested in batches of at most ten, and incomplete OCR produces an explicit
error rather than a successful partial document. Stored-document reads retain
their durable PostgreSQL cache and retry behavior described in [OCR](ocr.md).

Improvements over the original include preserving visible HTML after void
`meta`/`link` tags, retaining spaces between inline elements, escaping literal
pipe characters in table cells, retaining Word tables in PDF previews and
reporting truncated legacy Word streams instead of silently dropping text.
Word redline extraction keeps the existing canonical text convention so these
presentation changes do not invalidate tracked-edit anchors.

Processing is bounded: file-type caps are 50 MB for PDF, 25 MB for office/email/RTF
and 10 MB for plain-text formats, further limited by the instance upload setting.
ZIP files allow at most 10,000 entries and 250 MB expanded content; duplicate
entries and excessive XML nesting are rejected. Full text is limited to 4 MB.
Large tables have explicit row/column limits. Password protection must be removed
before importing.

This is not a claim of universal format fidelity. Legacy XLS versions before
BIFF8 and less common RTF encodings remain outside the native reader's coverage.
PDF preview is a readable rendering, not a reproduction of Word pagination,
images or typography; originals and GoodMem page images remain available. The
embedded PDF fonts have the script limits described in [chat exports](chat.md).
Native PDF table reconstruction and complete Word-host/browser acceptance remain
open parity work. Table categories supplied by GoodMem OCR are retained and
counted, while native PDF text is not assigned invented table counts.

## Source comparison and validation

`scripts/parity/text_extraction_fixtures.py` executes the pinned original
`text_extraction.py` against 39 fixtures written independently with Python office
libraries or a small CFB fixture writer. Fixtures include real DOCX, XLSX, XLS,
PPTX, DOC and MSG containers. Intentional improvements record both the Python
output and the corrected expectation with an explanation. The court list is
generated from the same pinned source. Python is development-only; ordinary Go
tests use checked-in data.

Additional PostgreSQL/API tests check authentication and custom permissions,
type/size validation, original-PDF pass-through, rendered text, GoodMem OCR page
coverage and tables, absence of permanent objects for temporary extraction, and
malformed archives/RTF/Word streams. Existing redline tests exercise the unchanged
canonical text path. The browser suite uses real GoodMem/PostgreSQL and the
original Contracts UI to upload Word, open its PDF preview, switch to text and
verify byte-identical original retrieval.

The legacy Excel reader is the pure-Go
[go-excel-reader](https://github.com/jloor/go-excel-reader) package, pinned to
v0.3.0. Other native readers use existing Go dependencies and the standard
library. Its MIT/ExcelDataReader attribution is included in `NOTICE.md`.
