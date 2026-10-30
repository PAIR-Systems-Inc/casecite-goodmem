# Chat migration

Shared chat, strategy routing, durable conversation publication, and Word/PDF/
Markdown transcript exports are implemented. The original React and Word
consumers remain the acceptance baseline. The tested flows below do not establish
complete application parity or live model quality.

The reference is Thomas Unise's Python implementation at
`6605cb3474cf0c2894080af216d5fed2a81fc997`. The development-only
`scripts/parity/chat_fixtures.py` evaluates its routing, query parsing, BM25,
rank fusion, compression, citation-reference, source-excerpt and reasoning
helpers. Its 160 cases and source hashes are checked in. Original classifier
instructions, routing patterns, and evidence-check instructions are embedded
in the Go binary. No Python runtime or separate keyword-search service is added.

## API and conversation persistence

`POST /chat` retains the synchronous response contract. `POST /chat?async=true`
returns a durable job; the browser polls it and cancels it through the jobs API.
`POST /strategy/brief` also accepts optional asynchronous execution. The queue
enforces the existing five-interactive-job limit. Temporary polling failures do
not submit a second generation request.

Input schemas and citations have 167 request cases and four citation cases
executed against the original Pydantic definitions. Request validation discards
unknown internal fields. An empty explicit document selection stays empty, while
an omitted filter searches readable documents. Shared conversation scope is
restricted to its matter, so an answer cannot publish a private file into shared
history. Settings and memory revisions are captured when submitting a request;
access and revisions are checked again when publishing its answer.

Job completion, the user's question, the assistant's answer, and the conversation
count commit in one PostgreSQL transaction. Cancelled jobs cannot publish a
partial exchange. Deleting a conversation removes its jobs before its history,
preventing a late provider response from recreating it. Reading a completed job
or history rechecks access to its sources. Pure case-law research does not depend
on selected documents it did not use.

## Retrieval

Retrieval keeps an explicit empty selection empty, searches only SQL-authorized
GoodMem memory revisions, and maps OCR derivative memories back to their
original application document IDs. Selected files retain a share of the answer's
passage budget. Two optional query expansions merge using reciprocal rank fusion;
the original question's exclusions and metadata filters continue to apply.
BM25 keyword scoring is request-local over the vector candidate pool, matching
the original fallback search behavior.

GoodMem's `retrieve` stage emits weighted negative inner products. Chat retains
the raw score and stage, negates it for the legacy similarity control, and
clamps the display field to 0–1. This is not a probability that an answer is
correct. Multiple embedders, their weights and unnormalized vectors affect that
scale. The application does not label it cosine similarity.

When a GoodMem reranker is configured and the user's reranking toggle is enabled,
chat performs a second retrieval for reranker ordering. The rerank API response
does not contain the original vector scores; keeping the first retrieval avoids
applying a vector threshold to an unrelated reranker score. Only eligible
candidates from the first pass can enter the answer. Without a configured
reranker, keyword/vector order is retained and the response carries an explicit
notice. No reranker API request is made when the toggle is off.

Weak passages from explicitly selected files remain available and are marked.
For an unscoped query with no match above the threshold, at most three weak
matches survive, as in the original. A GoodMem failure is an error, never an
empty-library response. Context compression changes prompt copies only; source
passages remain intact.

## Evidence checks and per-file answers

Claim grounding reads the same native/OCR canonical source text used by review
and redlines. It checks up to six source files, 30,000 characters of each file
(opening plus retrieved-passage windows), the first 12,000 answer characters,
and 15 factual claims. Exact quote location and substantive support are separate
checks. The optional support pass reads surrounding language for conditions,
exceptions and cure periods, and reports supported, partial, contradicted or
unsupported claims. Missing labels and unavailable checks are disclosed.

Quotes that cannot be found cannot acquire a supported verdict merely because
the model repeated them. All browser/Word source offsets use UTF-16; page
locations come from canonical extraction metadata. Read authorization and
source revision are rechecked before successful grounding returns.

The per-file path supports 2–8 files, at most four concurrent map calls, a
60,000-character input cap per file, up to three quotes per file, and one
synthesis call. It uses the selected provider. Duplicate filenames do not hide
failed analyses; missing, failed and truncated files are disclosed separately.
Truncated files are never labeled as read in full. Case references absent from
the files are removed. Citation reasoning also verifies its proposed evidence
quotations before publishing them.

## Strategy and case-law research

Strategy covers at most 25 selected documents, with three passages per document,
an explicit list of missing files, and actual read counts. Unsupported strategy
points remain visible with an explanation. There are 39 original Python cases
for grouping, rounding, passage construction and reference joining.

Fast case lookup reads relevant windows of the first three opinions and screens
the returned leads. Its unreviewed fallback is labeled. Deep case research merges
overlapping propositions, caches each opinion read within the request, and
verifies an entire proposed quotation against the actual opinion. It never uses
a cluster ID to fetch an opinion or salvages an invented quotation by retaining
only its longest matching sentence.

Long opinions are read in overlapping 80,000-character windows (2,000 overlap).
`RESEARCH_MAX_WINDOWS_PER_OPINION` defaults to six and is bounded to 1–32.
At most six authority calls run concurrently. Early stopping, window caps and
phase deadlines report partial reading; completed verified authorities survive
a timeout. These are evidence checks, not assurance of current legal treatment.

## Transcript exports

`POST /chat/export` accepts the original bounded transcript schema and produces
DOCX, PDF or Markdown without a model call. Twenty-nine original Python cases
cover text conversion, Markdown and filenames. Word uses native OOXML lists;
PDF generation runs inside the Go binary with embedded fonts and page numbers.
No office server, browser renderer or font download is needed at runtime.

Four-page Word and PDF samples were rendered and every page visually inspected,
including list restarts, long URLs, source quotations, Greek and Cyrillic text,
and page breaks. Word rendering used the bundled artifact-runtime LibreOffice
for development verification only. The PDF font does not cover every script:
unsupported characters produce an explicit error suggesting Word or Markdown,
instead of silently losing text. Broader font coverage remains open.

## Intentional improvements

- Classifier strings such as `"false"` are interpreted as booleans instead of
  Python's truthy-string behavior. Fenced JSON remains accepted.
- Different passages sharing the first 80 characters are not deduplicated.
- Advertised filename, source, judge and other metadata filters are applied;
  the original post-filter stage ignored several parsed fields.
- A filename stem such as `a` cannot prove citation use by occurring inside
  `payment`. The three corresponding Python oracle expectations are explicitly
  overridden by regression tests for this defect.
- Query expansion and reranking cannot remove the selected-file coverage
  guarantee or bypass the original query filters.
- Coverage top-ups use the final answer budget rather than the larger reranker
  candidate pool, avoiding needless repeat searches of small selected files.
- GoodMem fallback results sort according to the stage actually returned,
  rather than assuming that a configured reranker necessarily ran.
- Case-law excerpt shortening retains relevant terms even within a single long
  OCR paragraph. Screening and citation cards no longer blindly truncate the
  opening, and both opinion and cluster identities are retained.
- Cancelling or deleting an in-flight conversation cannot leave half an exchange
  or resurrect deleted history. Source revocation also applies to saved answers.

The regression fixtures exercise actual PostgreSQL state and local HTTP model
responses, including false claims, missing support labels, invented quotations,
emoji source offsets, OCR memory identity, failed files and truncation. They do
not establish live model quality. The built-image browser flow additionally
answers from a scanned PDF through the real GoodMem OCR API, checks a verified
page citation, generates a strategy brief, downloads all three export formats,
and checks cancellation and persisted history. Its OCR and generation models
are deterministic local fixtures.
