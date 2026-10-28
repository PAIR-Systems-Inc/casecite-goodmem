"""``review_tables`` and ``review_cells``: tabular review across many documents.

A review table stores a user-defined field schema and a document scope; a
cell stores one field's value for one document together with the verbatim
quote and offsets that support it. Both tables are inspector-guarded so an
install whose runtime create_all backstop already built them upgrades cleanly.

Revision ID: 015
Revises: 014
Create Date: 2026-10-28
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "015"
down_revision: str = "014"
branch_labels = None
depends_on = None


def _has_table(name: str) -> bool:
    return name in sa.inspect(op.get_bind()).get_table_names()


def upgrade() -> None:
    if not _has_table("review_tables"):
        op.create_table(
            "review_tables",
            sa.Column("id", sa.String(36), primary_key=True),
            sa.Column("owner_id", sa.String(64), nullable=False),
            sa.Column("matter_id", sa.String(36), nullable=True),
            sa.Column("name", sa.String(300), nullable=False),
            sa.Column("description", sa.Text(), nullable=True),
            sa.Column("fields", sa.JSON(), nullable=False),
            sa.Column("document_ids", sa.JSON(), nullable=False),
            sa.Column("last_run_at", sa.DateTime(), nullable=True),
            sa.Column("created_at", sa.DateTime(), nullable=False),
            sa.Column("updated_at", sa.DateTime(), nullable=True),
        )
        op.create_index("ix_review_tables_owner_id", "review_tables", ["owner_id"])
        op.create_index("ix_review_tables_matter_id", "review_tables", ["matter_id"])
        op.create_index(
            "idx_review_tables_owner_matter", "review_tables", ["owner_id", "matter_id"]
        )

    if not _has_table("review_cells"):
        op.create_table(
            "review_cells",
            sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
            sa.Column(
                "table_id",
                sa.String(36),
                sa.ForeignKey("review_tables.id", ondelete="CASCADE"),
                nullable=False,
            ),
            sa.Column("document_id", sa.String(64), nullable=False),
            sa.Column("field_key", sa.String(80), nullable=False),
            sa.Column("value", sa.Text(), nullable=True),
            sa.Column("quote", sa.Text(), nullable=True),
            sa.Column("span_start", sa.Integer(), nullable=True),
            sa.Column("span_end", sa.Integer(), nullable=True),
            sa.Column("verified", sa.Boolean(), nullable=False),
            sa.Column("status", sa.String(20), nullable=False),
            sa.Column("error", sa.Text(), nullable=True),
            sa.Column("model_used", sa.String(80), nullable=True),
            sa.Column("text_len", sa.Integer(), nullable=True),
            sa.Column("updated_at", sa.DateTime(), nullable=True),
            sa.UniqueConstraint("table_id", "document_id", "field_key", name="uq_review_cell"),
        )
        op.create_index("ix_review_cells_table_id", "review_cells", ["table_id"])
        op.create_index("idx_review_cells_table_doc", "review_cells", ["table_id", "document_id"])


def downgrade() -> None:
    if _has_table("review_cells"):
        op.drop_table("review_cells")
    if _has_table("review_tables"):
        op.drop_table("review_tables")
