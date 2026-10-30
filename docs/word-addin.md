# Word add-in and tracked edits

The original React task pane is embedded at `/word-addin/`; its deployment
manifest is `/word-addin/manifest.xml`. It uses the same Go application and
PostgreSQL sessions as the web interface. An embedded pane defaults to its own
server origin. A separately hosted pane can save the CaseCite server address.

Sign-in uses the original device-code flow: the pane displays a short code,
the user approves it in the authenticated web application, and the pane receives
rotating bearer/refresh tokens. Office requests omit browser cookies. Existing
password-change, MFA and active-account gates remain in the Go API.
Sign-out clears the pane immediately. Delayed analysis, research, matter listings
and token refreshes from an old session cannot repopulate it or replace another
account's tokens. Changing matter clears the old answer; cancelled device flows
cannot install a late approval. Saved tokens are restored only for their server.

Analysis reads Word's body text, runs the full contract pipeline and shows key
terms, issues and source quotations. Research uses the selected matter's document
IDs with GoodMem retrieval and the shared chat pipeline. Answers can be inserted
with source footnotes; hosts without the footnote API receive a Sources list.

## Exact replacement behavior

The original replacement function reused its navigation search, which could
shorten a long quote or fall back to its first sentence. It also chose the first
match when a passage occurred more than once. Those behaviors are retained only
for read-only navigation, not for tracked replacements.

A replacement must match the complete quotation, including its punctuation and
case; layout whitespace may differ. Literal carets are escaped for Word's search
syntax. Long passages use bounded beginning/end searches, combine the resulting
ranges, then verify all intervening text. They can span multiple paragraphs.

A full current selection resolves repeated passages. Otherwise an ambiguous
match leaves the document unchanged and asks the user to select the complete
intended passage. A shortened selection cannot authorize a larger replacement.
The live range is rechecked before writing. Pane mutations run in sequence and
duplicate pending rewrite/insert actions are ignored.
An applied finding is marked in the pane and cannot be inserted a second time
into another identical clause. Selecting it locates the replacement; a fresh
analysis resets these markers.

Changes enable Track All for the insertion and then restore the prior mode.
If insertion succeeded but restoring the mode fails, the pane says the edit was
inserted and reports the tracking-mode problem. Office batches are not database
transactions; host or coauthor changes can still race the final API operation.

The web contract viewer also honors a validated server span when identical
clauses recur. Stale spans cannot cause it to guess between repeated matches.
DOCX exports retain the original package and independently verify that rejecting
new revisions recovers its original visible text. Existing review notices are
not appended a second time.

Implementation references: original `word-addin/src/office/{document,search}.ts`,
`store.ts`, the analysis/research consumers and Python contract/chat/device
routes; Microsoft's [range API](https://learn.microsoft.com/en-us/javascript/api/word/word.range?view=office-js)
and [literal search syntax](https://learn.microsoft.com/en-us/office/dev/add-ins/word/search-option-guidance).
The range operations fit the existing WordApi 1.4 manifest requirement.

## Verification and limits

Position-based host tests cover long/repeated clauses, selections, Unicode,
carets, whitespace, changed text, bounded searches, serialized writes and
tracking-mode recovery. Store tests check rejection explanations, duplicate
admission and session/matter changes during requests. These extend the original
API, device-flow, storage and search tests (66 tests in seven files).

The browser test runs the actual embedded pane against the built Go server and
real GoodMem/PostgreSQL, with a simulated Office bridge and controlled model
responses. It covers device sign-in, a complete grounded analysis, ambiguous
replacement refusal, explicit selection of the second repeated long clause,
tracked insertion/restoration, matter-scoped research and source footnotes.
This is integration coverage, not a substitute for a live Microsoft Word run.

The two-page contract report and its tracked-change variant were rendered with
an independent LibreOffice engine and every page inspected. Existing tests check
accepted/rejected text, style/run preservation, existing revisions and OOXML
package contents. Microsoft Word desktop/web host acceptance and live Microsoft
tenant/provider checks remain outstanding.
