# Chat migration

The shared chat HTTP endpoint, strategy routing, durable conversation publication,
and transcript exports are still being integrated. The components below are
implemented and tested internally; this is not a claim of completed chat or UI
parity. The original React and Word consumers remain the acceptance baseline.

The reference is Thomas Unise's Python implementation at
`6605cb3474cf0c2894080af216d5fed2a81fc997`. The development-only
`scripts/parity/chat_fixtures.py` evaluates its routing, query parsing, BM25,
rank fusion, compression, citation-reference, source-excerpt and reasoning
helpers. Its 160 cases and source hashes are checked in. Original classifier
instructions, routing patterns, and evidence-check instructions are embedded
in the Go binary. No Python runtime or separate keyword-search service is added.

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

The regression fixtures exercise actual PostgreSQL state and local HTTP model
responses, including false claims, missing support labels, invented quotations,
emoji source offsets, OCR memory identity, failed files and truncation. They do
not establish live model quality or completed browser-level chat acceptance.
