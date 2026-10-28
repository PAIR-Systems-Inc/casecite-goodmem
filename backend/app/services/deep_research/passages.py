"""Server-assigned passage references and the code that joins them back.

The model never names a source. Every passage a tool retrieves is given a
reference (``P1``, ``P2``, ...) by the server, the model is told to cite only
those, and after each call the references in its output are joined back to
the real passages here: an unknown reference is stripped and counted, never
shown. A quote the model attributes to a passage is verified against that
passage's text; one that cannot be located is kept but flagged unverified.

This is the same discipline as ``strategy.py``'s ``join_citations``, applied
to free text (an answer with inline ``[P3]`` markers) as well as to
structured claims.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

from app.services.case_law_research import verify_quote

REF_RE = re.compile(r"^P\d+$")
_BRACKET_RE = re.compile(r"\[([^\[\]]{1,120})\]")
# Shown beside a citation; the model reads the full passage text.
SNIPPET_MAX_CHARS = 400


@dataclass
class Passage:
    """One retrieved passage with the metadata needed to cite it."""

    ref: str
    source_type: str  # "document" | "case_law"
    text: str  # the text the model saw; quotes are verified against it
    title: str  # filename or case name
    excerpt_source: str = "index"  # "index" | "opinion" | "snippet"
    document_id: str | None = None
    chunk_index: int | None = None
    opinion_id: str | None = None
    cluster_id: str | None = None
    citation: str | None = None
    url: str | None = None
    court: str | None = None
    date_filed: str | None = None
    score: float | None = None
    sub_question_ids: list[str] = field(default_factory=list)

    def source_entry(self) -> dict[str, Any]:
        """The citation-shaped dict the result carries for this passage."""
        return {
            "ref": self.ref,
            "type": self.source_type,
            "title": self.title,
            "snippet": self.text[:SNIPPET_MAX_CHARS],
            "excerpt_source": self.excerpt_source,
            "document_id": self.document_id,
            "chunk_index": self.chunk_index,
            "opinion_id": self.opinion_id,
            "cluster_id": self.cluster_id,
            "citation": self.citation,
            "url": self.url,
            "court": self.court,
            "date_filed": self.date_filed,
            "score": self.score,
            "sub_question_ids": list(self.sub_question_ids),
        }


class PassageStore:
    """Assigns references, de-duplicates passages and enforces the passage cap."""

    def __init__(self, max_passages: int) -> None:
        self.max_passages = max(1, int(max_passages))
        self._by_ref: dict[str, Passage] = {}
        self._by_key: dict[str, Passage] = {}
        self.dropped_over_cap = 0

    def __len__(self) -> int:
        return len(self._by_ref)

    @property
    def full(self) -> bool:
        return len(self._by_ref) >= self.max_passages

    def add(self, key: str, sub_question_id: str, **fields: Any) -> Passage | None:
        """Add a passage (or re-use the one already stored under ``key``).

        Returns None when the cap is reached, counting the drop so the result
        can say so.
        """
        existing = self._by_key.get(key)
        if existing is not None:
            if sub_question_id not in existing.sub_question_ids:
                existing.sub_question_ids.append(sub_question_id)
            return existing
        if self.full:
            self.dropped_over_cap += 1
            return None
        ref = f"P{len(self._by_ref) + 1}"
        passage = Passage(ref=ref, sub_question_ids=[sub_question_id], **fields)
        self._by_ref[ref] = passage
        self._by_key[key] = passage
        return passage

    def get(self, ref: str) -> Passage | None:
        return self._by_ref.get(ref)

    def all(self) -> list[Passage]:
        return list(self._by_ref.values())

    def for_sub_question(self, sub_question_id: str) -> list[Passage]:
        return [p for p in self._by_ref.values() if sub_question_id in p.sub_question_ids]


def extract_refs(value: Any) -> list[str]:
    """The ``P<n>`` tokens in a list the model returned (strings only, de-duplicated)."""
    refs: list[str] = []
    for item in value if isinstance(value, list) else []:
        token = str(item).strip().strip("[]").strip()
        if REF_RE.match(token) and token not in refs:
            refs.append(token)
    return refs


def join_refs_in_text(text: str, allowed: set[str]) -> tuple[str, list[str], int]:
    """Keep only allowed ``[P<n>]`` markers in free text.

    Bracket groups are scanned (``[P1]``, ``[P1, P4]``); markers whose
    reference is not in ``allowed`` are removed, a group left empty disappears,
    and bracketed text that is not a reference list is left alone. Returns
    (clean_text, cited_refs_in_order, stripped_count).
    """
    cited: list[str] = []
    stripped = 0

    def replace(match: re.Match[str]) -> str:
        nonlocal stripped
        parts = [p.strip() for p in match.group(1).split(",")]
        if not parts or not all(REF_RE.match(p) for p in parts):
            return match.group(0)  # ordinary bracketed text, not a reference list
        kept: list[str] = []
        for ref in parts:
            if ref in allowed:
                kept.append(ref)
                if ref not in cited:
                    cited.append(ref)
            else:
                stripped += 1
        return f"[{', '.join(kept)}]" if kept else ""

    clean = _BRACKET_RE.sub(replace, text or "")
    clean = re.sub(r" {2,}", " ", clean)
    clean = re.sub(r" +([.,;:])", r"\1", clean)
    return clean.strip(), cited, stripped


def verify_claim_quote(store: PassageStore, refs: list[str], quote: str) -> dict[str, Any]:
    """Check a quote against the passages it is attributed to.

    Returns {"verified": bool, "ref": the passage it was found in or None}.
    An empty quote is neither verified nor unverified — it is a claim with
    no quotation, reported as such by the caller.
    """
    quote = (quote or "").strip()
    if not quote:
        return {"verified": False, "ref": None, "empty": True}
    for ref in refs:
        passage = store.get(ref)
        if passage is not None and verify_quote(passage.text, quote) is not None:
            return {"verified": True, "ref": ref, "empty": False}
    return {"verified": False, "ref": None, "empty": False}
