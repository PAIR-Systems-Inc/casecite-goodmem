# Document extraction and previews

The Go server reads full document text for analysis, comparisons, citations and
exports. GoodMem keeps the original bytes, page images and search index. Native
readers do not add a second document store or an office-conversion service.

| Format | Go extraction |
| --- | --- |
| PDF | Native text and ruled tables, including merged cells, wrapped text and transformed grids; GoodMem's on-premise OCR for scans, forms and unsupported text layouts |
| DOCX | Canonical visible text for exact redline anchors; interleaved paragraphs, headings and tables for upload extraction and PDF previews |
| DOC | Word 97–2003 OLE piece tables, including mixed Windows-1252 and UTF-16 runs; RTF/DOCX files saved with a `.doc` name are recognized |
| XLSX | All worksheets, cached formula values and formatted cell values |
| XLS | Excel 2–95 BIFF2/3/4/4W/5 and Excel 97–2003 BIFF8; raw or OLE workbooks, worksheets, cached formula results and typed values |
| PPTX | Presentation order, grouped shape text, tables and speaker notes; slide boundaries remain available for citations |
| ODT | Paragraphs, headings, inline text, explicit spaces, tabs and line breaks |
| CSV / TSV | Quoted cells, variable row widths and Markdown tables |
| HTML | Visible text, entities, paragraph boundaries and inline whitespace |
| EML | Decoded headers, MIME bodies, preferred plain-text alternatives and attachment names |
| MSG | Outlook property streams, message headers/date/body and attachment names |
| RTF | Scoped groups, document and per-font code pages, Unicode/surrogate pairs, table separators and displayed field results |
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
Native PDF extraction keeps paragraphs between tables in their physical reading
order; the original reader moved all surrounding text before the tables. PDF
forms and rotated text use GoodMem OCR instead of accepting incomplete headers
or incorrectly ordered letters. Recognized native tables need no OCR call.
Word redline extraction keeps the existing canonical text convention so these
presentation changes do not invalidate tracked-edit anchors.

Older Excel files reuse the durable text-index job used for scans. GoodMem keeps
the original workbook while a second memory indexes the complete native Go
decoding. This handles files rejected by GoodMem's default parser without an OCR
call or an additional service. Search citations still identify the original
document; retry, rebuild and deletion cover both memories. The Knowledge Base
offers **Retry indexing** for a failed native index. Macros, formula programs and
external workbook links are never executed; only stored formula results are read.

The Contracts text viewer renders extracted Markdown tables as rows and columns,
including escaped literal pipes and backslashes. Cell selections map back to the
canonical source; citation highlights and single-cell redlines remain anchored.
An edit crossing table delimiters retains a verbatim view so its replacement
stays intact. The editor and downloads keep the complete source text. Normalized
quote highlighting now stops at the exact boundary instead of extending beyond it.

Processing is bounded: file-type caps are 50 MB for PDF, 25 MB for office/email/RTF
and 10 MB for plain-text formats, further limited by the instance upload setting.
ZIP files allow at most 10,000 entries and 250 MB expanded content; duplicate
entries and excessive XML nesting are rejected. Full text is limited to 4 MB.
Large tables have explicit row/column limits. Password protection must be removed
before importing.

This is not a claim of universal format fidelity. Unmapped legacy character
encodings fail explicitly and recommend conversion instead of corrupting text.
PDF preview is a readable rendering, not a reproduction of Word pagination,
images or typography; originals and GoodMem page images remain available. The
embedded PDF fonts have the script limits described in [chat exports](chat.md).
Unruled or unusual PDF table layouts and complete Word-host/browser acceptance
remain open fidelity work. Native table counts come from detected grids;
GoodMem's table categories are retained and counted. GoodMem OCR's HTML tables
are normalized before indexing and offset calculation, without another model call.

## Source comparison and validation

`scripts/parity/text_extraction_fixtures.py` executes the pinned original
`text_extraction.py` against 57 fixtures written independently with Python office
libraries or a small CFB fixture writer. Fixtures include real DOCX, XLSX, XLS,
PPTX, DOC and MSG containers. Intentional improvements record both the Python
output and the corrected expectation with an explanation. The court list is
generated from the same pinned source. Python is development-only; ordinary Go
tests use checked-in data.

The legacy Excel cases include raw BIFF2/3/4, BIFF4W, OLE and raw BIFF5,
multiple worksheets, cached formula strings/values, continuation records,
embedded charts and Cyrillic/Japanese/Macintosh code pages. RTF cases include
per-font encodings and corrections to the original reader's raw-markup fallback
for valid code pages. A separate optional comparison also passed against the five
[published OpenOffice Excel samples](https://www.openoffice.org/sc/testdocs/filetype/).
These external binaries are not redistributed. The bounded legacy parser passed
328,533 fuzz inputs in 20 seconds, as well as malformed-record regression tests.

`scripts/parity/pdf_table_fixtures.py` adds 12 independently generated PDF cases
compared with the original pdfplumber path: ruled/rectangle/transformed grids,
split rules, horizontal and vertical merged cells, wrapped/empty cells, multiple
tables, punctuation, an isolated box, embedded forms and rotated text. Tests
check exact cell values, physical text order, conservative OCR routing and text
limits without unnecessary OCR work. Every fixture page was rendered and inspected.

Additional PostgreSQL/API tests check authentication and custom permissions,
type/size validation, original-PDF pass-through, rendered text, GoodMem OCR page
coverage and tables, absence of permanent objects for temporary extraction, and
malformed archives/RTF/Word streams. Existing redline tests exercise the unchanged
canonical text path. The browser suite uses real GoodMem/PostgreSQL and the
original Contracts UI to upload Word, open its PDF preview, switch to text and
verify byte-identical original retrieval. It also displays a native PDF table on
desktop and phone, checks all ten native PDF cases through the API, and retrieves
the original PDF and its page image from real GoodMem.
It also indexes legacy Excel through real GoodMem, reads the complete native
text, retrieves byte-identical originals and answers a document-scoped question
with a citation to the original workbook.

The BIFF8 reader is the pure-Go
[go-excel-reader](https://github.com/jloor/go-excel-reader) package, pinned to
v0.3.0. The bounded BIFF2–5 reader follows the
[OpenOffice file-format specification](https://www.openoffice.org/sc/excelfileformat.pdf).
Other native readers use existing Go dependencies and the standard library.
The package's MIT/ExcelDataReader attribution is included in `NOTICE.md`.
