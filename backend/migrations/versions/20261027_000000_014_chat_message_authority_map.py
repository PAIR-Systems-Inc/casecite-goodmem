"""``authority_map`` on ``chat_messages``: the chat-triggered authority map's record.

When a chat message routes to the exhaustive authority mapper, the mapper's
runs are stored (``authority_map_runs``) but nothing tied them to the
conversation, so a reopened session showed only the "Mapping supporting case
law…" text. The assistant message now carries a JSON record of the job, the
scoped documents and, once finished, the run ids and totals.

Nullable JSON, inspector-guarded so installs whose runtime backstop already
built the column upgrade cleanly; no backfill is needed.

Revision ID: 014
Revises: 013
Create Date: 2026-10-27
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "014"
down_revision: str = "013"
branch_labels = None
depends_on = None


def _existing_columns(table: str) -> set[str]:
    return {c["name"] for c in sa.inspect(op.get_bind()).get_columns(table)}


def upgrade() -> None:
    if "authority_map" not in _existing_columns("chat_messages"):
        op.add_column("chat_messages", sa.Column("authority_map", sa.JSON(), nullable=True))


def downgrade() -> None:
    if "authority_map" in _existing_columns("chat_messages"):
        op.drop_column("chat_messages", "authority_map")
