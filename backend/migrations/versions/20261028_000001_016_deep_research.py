"""``deep_research_runs``: planned multi-step research runs and their results.

One row per run: the question, the plan the user approved, the document scope
searched, the run's status and the result JSON (answer, step log, sources,
scope and verification counts). Inspector-guarded so an install whose runtime
create_all backstop already built the table upgrades cleanly.

Revision ID: 016
Revises: 015
Create Date: 2026-10-28
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "016"
down_revision: str = "015"
branch_labels = None
depends_on = None


def _has_table(name: str) -> bool:
    return name in sa.inspect(op.get_bind()).get_table_names()


def upgrade() -> None:
    if not _has_table("deep_research_runs"):
        op.create_table(
            "deep_research_runs",
            sa.Column("id", sa.String(36), primary_key=True),
            sa.Column("owner_id", sa.String(64), nullable=False),
            sa.Column("matter_id", sa.String(36), nullable=True),
            sa.Column("question", sa.Text(), nullable=False),
            sa.Column("plan", sa.JSON(), nullable=False),
            sa.Column("scope", sa.JSON(), nullable=False),
            sa.Column("status", sa.String(20), nullable=False),
            sa.Column("job_id", sa.String(36), nullable=True),
            sa.Column("result", sa.JSON(), nullable=True),
            sa.Column("error", sa.Text(), nullable=True),
            sa.Column("created_at", sa.DateTime(), nullable=False),
            sa.Column("completed_at", sa.DateTime(), nullable=True),
        )
        op.create_index("ix_deep_research_runs_owner_id", "deep_research_runs", ["owner_id"])
        op.create_index("ix_deep_research_runs_matter_id", "deep_research_runs", ["matter_id"])
        op.create_index(
            "idx_deep_research_owner_matter", "deep_research_runs", ["owner_id", "matter_id"]
        )


def downgrade() -> None:
    if _has_table("deep_research_runs"):
        op.drop_table("deep_research_runs")
