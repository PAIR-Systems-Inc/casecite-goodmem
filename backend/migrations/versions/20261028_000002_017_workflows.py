"""``workflows``, ``workflow_runs`` and ``workflow_step_runs``: saved,
repeatable sequences of the platform's own operations and their executions.

A workflow stores an ordered list of typed steps; a run stores the steps as
they were when it started plus its input documents and status; a step run
stores one step's status, output and what it produced. Inspector-guarded so an
install whose runtime create_all backstop already built the tables upgrades
cleanly.

Revision ID: 017
Revises: 016
Create Date: 2026-10-28
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "017"
down_revision: str = "016"
branch_labels = None
depends_on = None


def _has_table(name: str) -> bool:
    return name in sa.inspect(op.get_bind()).get_table_names()


def upgrade() -> None:
    if not _has_table("workflows"):
        op.create_table(
            "workflows",
            sa.Column("id", sa.String(36), primary_key=True),
            sa.Column("owner_id", sa.String(64), nullable=False),
            sa.Column("matter_id", sa.String(36), nullable=True),
            sa.Column("name", sa.String(300), nullable=False),
            sa.Column("description", sa.Text(), nullable=True),
            sa.Column("template_key", sa.String(60), nullable=True),
            sa.Column("steps", sa.JSON(), nullable=False),
            sa.Column("last_run_at", sa.DateTime(), nullable=True),
            sa.Column("created_at", sa.DateTime(), nullable=False),
            sa.Column("updated_at", sa.DateTime(), nullable=True),
        )
        op.create_index("ix_workflows_owner_id", "workflows", ["owner_id"])
        op.create_index("ix_workflows_matter_id", "workflows", ["matter_id"])
        op.create_index("idx_workflows_owner_matter", "workflows", ["owner_id", "matter_id"])

    if not _has_table("workflow_runs"):
        op.create_table(
            "workflow_runs",
            sa.Column("id", sa.String(36), primary_key=True),
            sa.Column(
                "workflow_id",
                sa.String(36),
                sa.ForeignKey("workflows.id", ondelete="CASCADE"),
                nullable=False,
            ),
            sa.Column("runner_id", sa.String(64), nullable=False),
            sa.Column("matter_id", sa.String(36), nullable=True),
            sa.Column("steps", sa.JSON(), nullable=False),
            sa.Column("input_document_ids", sa.JSON(), nullable=False),
            sa.Column("status", sa.String(20), nullable=False),
            sa.Column("job_id", sa.String(36), nullable=True),
            sa.Column("error", sa.Text(), nullable=True),
            sa.Column("resumed_count", sa.Integer(), nullable=False, server_default="0"),
            sa.Column("created_at", sa.DateTime(), nullable=False),
            sa.Column("completed_at", sa.DateTime(), nullable=True),
        )
        op.create_index("ix_workflow_runs_workflow_id", "workflow_runs", ["workflow_id"])
        op.create_index("ix_workflow_runs_runner_id", "workflow_runs", ["runner_id"])
        op.create_index(
            "idx_workflow_runs_workflow_created", "workflow_runs", ["workflow_id", "created_at"]
        )

    if not _has_table("workflow_step_runs"):
        op.create_table(
            "workflow_step_runs",
            sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
            sa.Column(
                "run_id",
                sa.String(36),
                sa.ForeignKey("workflow_runs.id", ondelete="CASCADE"),
                nullable=False,
            ),
            sa.Column("index", sa.Integer(), nullable=False),
            sa.Column("name", sa.String(80), nullable=False),
            sa.Column("kind", sa.String(40), nullable=False),
            sa.Column("status", sa.String(20), nullable=False),
            sa.Column("output", sa.JSON(), nullable=True),
            sa.Column("input_document_ids", sa.JSON(), nullable=False),
            sa.Column("produced_ids", sa.JSON(), nullable=False),
            sa.Column("error", sa.Text(), nullable=True),
            sa.Column("started_at", sa.DateTime(), nullable=True),
            sa.Column("finished_at", sa.DateTime(), nullable=True),
        )
        op.create_index("ix_workflow_step_runs_run_id", "workflow_step_runs", ["run_id"])
        op.create_index(
            "idx_workflow_step_runs_run_index",
            "workflow_step_runs",
            ["run_id", "index"],
            unique=True,
        )


def downgrade() -> None:
    for name in ("workflow_step_runs", "workflow_runs", "workflows"):
        if _has_table(name):
            op.drop_table(name)
