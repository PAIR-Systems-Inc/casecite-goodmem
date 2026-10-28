"""The fixed tool set a deep-research run may call.

Every tool wraps an existing service and returns passages for the store to
reference; nothing here talks to a provider the rest of the application does
not already use. There is no open-ended tool calling: the planner chooses
from this list, the runner calls what the approved plan allows, and a budget
caps the total number of calls in a run.

- firm documents: ``rag.search.search_documents`` with the same tenant scope
  and document filter research chat uses;
- case law: CourtListener opinion search; for each candidate the opinion is
  fetched and the passage most relevant to the sub-question is cut from its
  real text, so a quote can be verified against what the court wrote (the
  search snippet is used only when the opinion text is unavailable);
- citation check: ``courtlistener_service.validate_citation`` for authorities
  the final answer cites (the treatment-language scan — not a citator).

Connector-backed fetches are deliberately absent: the connector services
import files into the index; there is no read-a-document-on-demand path to
wrap, and inventing one here would bypass the index's tenant scoping.
"""

from __future__ import annotations

import html
import logging
import re
from dataclasses import dataclass
from typing import Any

from app.services.contract_analysis.long_drafting import _relevance, _terms
from app.services.courtlistener import courtlistener_service
from app.services.rag.search import SearchUnavailableError, search_documents
from app.services.vectordb import get_vector_db

logger = logging.getLogger(__name__)

# Passages per firm-document search and candidates per case-law search.
DOCUMENT_TOP_K = 6
CASE_LAW_CANDIDATES = 3
# A stored chunk is ~512 tokens; an opinion excerpt is cut to this length.
PASSAGE_MAX_CHARS = 4_000
_TAG_RE = re.compile(r"<[^>]+>")
_PARA_SPLIT_RE = re.compile(r"\n\s*\n")


class ToolBudgetExhausted(RuntimeError):
    """The run has made as many tool calls as it is allowed."""


@dataclass
class ToolBudget:
    """Counts tool calls across a whole run; ``take`` refuses past the cap."""

    max_calls: int
    used: int = 0

    def take(self) -> None:
        if self.used >= self.max_calls:
            raise ToolBudgetExhausted(f"The run reached its limit of {self.max_calls} tool calls.")
        self.used += 1

    @property
    def remaining(self) -> int:
        return max(0, self.max_calls - self.used)


def strip_html(value: str | None) -> str:
    return html.unescape(_TAG_RE.sub(" ", value or "")).strip()


def best_excerpt(text: str, query: str, max_chars: int = PASSAGE_MAX_CHARS) -> str:
    """A contiguous slice of ``text`` around the paragraph most about ``query``.

    The slice is cut from the original text (not re-joined), so a quote the
    model copies from it verifies against the opinion verbatim.
    """
    if len(text) <= max_chars:
        return text
    focus = _terms(query)
    paragraphs: list[tuple[int, int]] = []
    pos = 0
    for sep in _PARA_SPLIT_RE.finditer(text):
        if sep.start() > pos:
            paragraphs.append((pos, sep.start()))
        pos = sep.end()
    if pos < len(text):
        paragraphs.append((pos, len(text)))
    if not paragraphs:
        return text[:max_chars]
    best = max(paragraphs, key=lambda span: _relevance(text[span[0] : span[1]], focus))
    centre = (best[0] + best[1]) // 2
    start = max(0, min(centre - max_chars // 2, len(text) - max_chars))
    return text[start : start + max_chars]


async def search_firm_documents(
    query: str,
    *,
    user_id: str,
    user_keys: Any,
    document_ids: list[str] | None,
    matter_ids: list[str] | None,
    top_k: int = DOCUMENT_TOP_K,
) -> list[dict[str, Any]]:
    """Semantic search over the caller's documents; raw search results.

    ``document_ids`` None searches everything the caller may see; an empty
    list searches nothing (a scope that matched no documents never widens).
    """
    vector_db = get_vector_db()
    if vector_db is None:
        raise SearchUnavailableError("The document index is unavailable.")
    return await search_documents(
        vector_db,
        query,
        top_k=top_k,
        user_keys=user_keys,
        document_ids=document_ids,
        user_id=user_id,
        matter_ids=matter_ids,
    )


async def _opinion_excerpt(opinion: Any, query: str) -> tuple[str, str]:
    """(passage text, where it came from) for one search hit."""
    try:
        full = await courtlistener_service.get_opinion(opinion.id)
    except Exception as e:  # per-candidate; the snippet is the fallback
        logger.warning("Deep research: opinion %s fetch failed: %s", opinion.id, e)
        full = None
    text = (getattr(full, "text", None) or "") if full else ""
    if text.strip():
        return best_excerpt(text, query), "opinion"
    return strip_html(getattr(opinion, "snippet", None)), "snippet"


async def search_case_law(query: str, *, limit: int = CASE_LAW_CANDIDATES) -> list[dict[str, Any]]:
    """CourtListener opinion search with each hit's most relevant passage.

    Returns passage dicts: case_name, citation, opinion_id, cluster_id, url,
    court, date_filed, text, excerpt_source, score. Raises on search failure
    (the caller records the error on the step); a failed opinion fetch
    degrades that one hit to its snippet.
    """
    opinions = await courtlistener_service.search_opinions(query=query, limit=limit, cited_gt=2)
    out: list[dict[str, Any]] = []
    for opinion in (opinions or [])[:limit]:
        text, excerpt_source = await _opinion_excerpt(opinion, query)
        if not text:
            continue
        cites = getattr(opinion, "citation", None) or []
        citation = str(cites[0]) if cites else None
        date_filed = getattr(opinion, "date_filed", None)
        out.append(
            {
                "case_name": opinion.case_name,
                "citation": citation,
                "opinion_id": str(opinion.id),
                "cluster_id": str(opinion.cluster_id)
                if getattr(opinion, "cluster_id", None)
                else None,
                "url": getattr(opinion, "absolute_url", None),
                "court": getattr(opinion, "court", None) or None,
                "date_filed": str(date_filed) if date_filed else None,
                "text": text,
                "excerpt_source": excerpt_source,
                "score": getattr(opinion, "score", None),
            }
        )
    return out


async def check_citation(citation: str) -> dict[str, Any]:
    """Treatment-language scan for one reporter citation. Never raises."""
    try:
        result = await courtlistener_service.validate_citation(citation)
    except ValueError as e:  # no CourtListener token configured
        return {"citation": citation, "status": "unavailable", "detail": str(e)[:200]}
    except Exception as e:  # network, rate limit, upstream error
        logger.warning("Deep research: citation check for %r failed: %s", citation, e)
        return {"citation": citation, "status": "error", "detail": type(e).__name__}
    return {
        "citation": citation,
        "status": "checked",
        "case_name": result.case_name,
        "warning_level": result.warning_level,
        "is_good_law": result.is_good_law,
        "url": result.absolute_url,
        "negative": len(result.negative_citations),
        "caution": len(result.caution_citations),
    }
