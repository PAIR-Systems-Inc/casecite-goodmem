"""Tabular review models — one schema, many contracts, one cited cell per field.

A ``ReviewTable`` is a saved review: who owns it, which matter it belongs to,
the fields the reviewer wants extracted (a user-defined schema) and the
documents in scope. A ``ReviewCell`` is one (document, field) answer carrying
the value, the verbatim quote that supports it and where that quote sits in
the document text, so every exported figure can be traced back to its source.

These tables use SQLAlchemy 2.0 ``Mapped[]`` columns, so attribute reads are
typed as plain Python values (the older ``Column`` tables are not; see the
mypy ratchet notes).
"""

from datetime import datetime
from typing import Any

from sqlalchemy import (
    JSON,
    Boolean,
    DateTime,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
)
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base

# Cell statuses. ``extracted`` means the quote was found verbatim in the
# document; ``unverified`` means the model answered but its quote could not be
# located, so the value is shown de-emphasised and never as sourced.
CELL_EXTRACTED = "extracted"
CELL_UNVERIFIED = "unverified"
CELL_NOT_FOUND = "not_found"
CELL_ERROR = "error"
CELL_STATUSES = (CELL_EXTRACTED, CELL_UNVERIFIED, CELL_NOT_FOUND, CELL_ERROR)


class ReviewTable(Base):
    """A saved tabular review: schema + document scope, owned like other work product."""

    __tablename__ = "review_tables"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    # Owner and matter follow the other user-scoped tables (plain strings, no FK).
    owner_id: Mapped[str] = mapped_column(String(64), nullable=False, index=True)
    matter_id: Mapped[str | None] = mapped_column(String(36), nullable=True, index=True)
    name: Mapped[str] = mapped_column(String(300), nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)

    # Schema: list of {key, name, description, type}. ``key`` is the stable slug
    # cells are stored under; ``type`` is one of the ReviewFieldType values.
    fields: Mapped[list[dict[str, Any]]] = mapped_column(JSON, nullable=False, default=list)
    # Explicit document scope (ids), in review order.
    document_ids: Mapped[list[str]] = mapped_column(JSON, nullable=False, default=list)

    last_run_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow, nullable=False)
    updated_at: Mapped[datetime | None] = mapped_column(
        DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )

    __table_args__ = (Index("idx_review_tables_owner_matter", "owner_id", "matter_id"),)


class ReviewCell(Base):
    """One extracted field for one document, with its verifying quote."""

    __tablename__ = "review_cells"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    table_id: Mapped[str] = mapped_column(
        String(36), ForeignKey("review_tables.id", ondelete="CASCADE"), nullable=False, index=True
    )
    document_id: Mapped[str] = mapped_column(String(64), nullable=False)
    field_key: Mapped[str] = mapped_column(String(80), nullable=False)

    value: Mapped[str | None] = mapped_column(Text, nullable=True)
    quote: Mapped[str | None] = mapped_column(Text, nullable=True)
    span_start: Mapped[int | None] = mapped_column(Integer, nullable=True)
    span_end: Mapped[int | None] = mapped_column(Integer, nullable=True)
    verified: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    status: Mapped[str] = mapped_column(String(20), nullable=False, default=CELL_NOT_FOUND)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    model_used: Mapped[str | None] = mapped_column(String(80), nullable=True)
    # Length of the document text the span refers to; a re-indexed document
    # that changes length invalidates the offsets.
    text_len: Mapped[int | None] = mapped_column(Integer, nullable=True)

    updated_at: Mapped[datetime | None] = mapped_column(
        DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )

    __table_args__ = (
        UniqueConstraint("table_id", "document_id", "field_key", name="uq_review_cell"),
        Index("idx_review_cells_table_doc", "table_id", "document_id"),
    )
