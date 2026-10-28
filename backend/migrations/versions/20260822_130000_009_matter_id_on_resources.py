"""Add matter_id to the user-scoped SQL resources.

Adds a nullable matter_id column to chat_sessions, contract_analysis_runs,
authority_map_runs, and workspace_sessions so each can be filed under a shared
matter. NULL keeps the historical owner-only scope, so this revision is
backward-compatible and requires no backfill.

Revision ID: 009
Revises: 008
Create Date: 2026-08-22
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "009"
down_revision: str = "008"
branch_labels = None
depends_on = None

_TABLES = (
    "chat_sessions",
    "contract_analysis_runs",
    "authority_map_runs",
    "workspace_sessions",
)


def upgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    existing_tables = set(inspector.get_table_names())
    for table in _TABLES:
        if table not in existing_tables:
            continue
        columns = {c["name"] for c in inspector.get_columns(table)}
        if "matter_id" not in columns:
            op.add_column(table, sa.Column("matter_id", sa.String(length=36), nullable=True))
            op.create_index(f"idx_{table}_matter", table, ["matter_id"])


def downgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    existing_tables = set(inspector.get_table_names())
    for table in _TABLES:
        if table not in existing_tables:
            continue
        columns = {c["name"] for c in inspector.get_columns(table)}
        if "matter_id" not in columns:
            continue
        # Drop whatever index covers the column, by its actual name. A fresh
        # build never has ``idx_<table>_matter``: these tables are created from
        # the model metadata (004/006/007/011) with matter_id already present
        # and the model's ``ix_<table>_matter_id`` index, so upgrade() above
        # skipped them and an unconditional drop_index here fails.
        matter_indexes = [
            ix["name"] for ix in inspector.get_indexes(table) if ix["column_names"] == ["matter_id"]
        ]
        with op.batch_alter_table(table) as batch:
            for name in matter_indexes:
                batch.drop_index(name)
            batch.drop_column("matter_id")
