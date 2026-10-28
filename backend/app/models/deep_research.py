"""Deep research runs — a planned, multi-step research question and its result.

A ``DeepResearchRun`` records one run of the deep-research pipeline: the
question, the plan the user approved (sub-questions and the tools each may
use), the run's status, and the final result (answer, step log, sources and
scope) as JSON. Runs are owned like other work product: the owner always
reads them; members of the run's matter may read them too; nobody else sees
that they exist.

SQLAlchemy 2.0 ``Mapped[]`` columns, like ``review_tables``, so attribute reads
are typed as plain Python values.
"""

from datetime import datetime
from typing import Any

from sqlalchemy import JSON, DateTime, Index, String, Text
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base

RUN_RUNNING = "running"
RUN_COMPLETED = "completed"
RUN_FAILED = "failed"
RUN_CANCELLED = "cancelled"
RUN_STATUSES = (RUN_RUNNING, RUN_COMPLETED, RUN_FAILED, RUN_CANCELLED)


class DeepResearchRun(Base):
    """One deep-research run: question, approved plan, status and result."""

    __tablename__ = "deep_research_runs"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    owner_id: Mapped[str] = mapped_column(String(64), nullable=False, index=True)
    matter_id: Mapped[str | None] = mapped_column(String(36), nullable=True, index=True)
    question: Mapped[str] = mapped_column(Text, nullable=False)
    # The plan as run: [{id, question, tools: [...]}, ...].
    plan: Mapped[list[dict[str, Any]]] = mapped_column(JSON, nullable=False, default=list)
    # Document scope the run searched: {"document_ids": [...] | None, "matter_id": ...}.
    scope: Mapped[dict[str, Any]] = mapped_column(JSON, nullable=False, default=dict)
    status: Mapped[str] = mapped_column(String(20), nullable=False, default=RUN_RUNNING)
    job_id: Mapped[str | None] = mapped_column(String(36), nullable=True)
    result: Mapped[dict[str, Any] | None] = mapped_column(JSON, nullable=True)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)

    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow, nullable=False)
    completed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)

    __table_args__ = (Index("idx_deep_research_owner_matter", "owner_id", "matter_id"),)
