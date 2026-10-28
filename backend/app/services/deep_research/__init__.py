"""Deep research: plan a question into sub-questions, research each against a
fixed tool set, verify every quote against the retrieved text, and synthesise
an answer that can cite only server-assigned passage references.

See ``docs/deep-research.md`` for the pipeline and its caps.
"""

from app.services.deep_research.service import (
    DeepResearchError,
    DeepResearchService,
    PlanningError,
    deep_research_service,
)

__all__ = [
    "DeepResearchError",
    "DeepResearchService",
    "PlanningError",
    "deep_research_service",
]
