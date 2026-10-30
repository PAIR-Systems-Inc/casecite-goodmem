# Judge intelligence

The original judge screens use the Go server for CourtListener search, profile
builds and refreshes, biography, education, positions, opinions, assigned dockets,
statistics and the nine computed metrics. PostgreSQL holds the shared profile and
metrics caches, durable build jobs, and each user's brief/conversation history.

Briefs and Q&A use the shared chat API with `judge_id` and `judge_task` (`brief`
or `question`). The server loads the cached profile itself. The client does not
supply a biography, case allowlist or model instructions. A brief keeps the
original seven sections: quick facts, background, education, career and
appointments, judicial record, notable cases, and courtroom strategy. Questions
can use judge data alone, all accessible documents, or the existing document and
folder selector. Selected documents are retrieved from their GoodMem indexes.

The original profile context is retained: biography, educational history,
appointments, opinion counts/citations/date range, breakdowns by court and year,
most-cited opinions, assigned-case counts/types/recent dockets, and Wikipedia
biographical sections. Case names in those sources remain available to the
answer; references invented by the model are removed. Profile data establishes
that a reference was supplied, not that it supports a particular legal holding.

Verbatim quotation and support checks consider the profile alongside selected
documents. Profile citations have type `judge_profile`, a CourtListener person
URL, and no document ID, fabricated retrieval score or Word anchor. Document
citations retain their exact source anchors and access checks. Incomplete checks
are reported in the answer. These checks depend on model judgments and do not
prove a legal conclusion. Instructions explicitly prohibit predicting rulings
or judicial preferences from demographics, appointing officials or citation
counts alone, and require limits of the sampled data to be stated.

A request captures the profile's generation and revision. Refresh, deletion or
loss of judge permission prevents an obsolete answer from entering saved chat
history. Leaving the profile cancels pending brief/Q&A requests; clearing the
conversation cancels its pending question. Late search, statistics, metrics,
opinion and history responses cannot overwrite a newly opened judge. A failed
regeneration keeps the previous successful brief. Reopening History restores the
brief, questions and document scope, and subsequent saves update that same entry.

## Source comparisons and verification

The reference is original commit `6605cb3474cf0c2894080af216d5fed2a81fc997`:
`routers/judge_intel.py`, `services/judge_intel/*`, the judge router/identity/text
tests, `services/rag/service.py`, `services/rag/case_law_guard.py`,
`frontend/src/stores/judgeIntelStore.ts` and its actual view/components.

The existing 53 Python fixtures cover profile helpers, service queries, text and
metrics. Added Go/PostgreSQL tests cover authoritative brief/Q&A context,
selected-document grounding, source citations, request validation, queued
revision changes and atomic publication under refresh, deletion, permission
revocation and cancellation. The original case-reference oracle suite remains
in place; regression cases cover sentence boundaries and abbreviated party names.
Frontend tests cover cancellation, stale responses, history restoration,
continued autosaves and request failures. The real GoodMem/PostgreSQL browser
flow generates all seven brief sections, answers against one selected document,
reopens History, regenerates into the same saved entry and cancels a pending
question. Desktop and phone brief/Q&A screenshots were inspected.

CourtListener/Wikipedia and model responses in automated tests are controlled
fixtures. The tests establish API and UI behavior, not live data coverage or the
quality of a real model's judicial analysis.
