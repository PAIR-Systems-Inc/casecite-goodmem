"""Align the live schema with the models.

The migration-built schema had drifted from the models in ways create_all can
never repair (it only creates whole tables, never adds columns):

- users was missing mfa_secret / mfa_recovery_codes / password_changed_at —
  on a fresh migration-built database, TOTP MFA and password-change token
  invalidation would fail at runtime (get_current_user selects
  password_changed_at on every request).
- audit_logs was missing entry_hash / previous_hash / correlation_id — the
  tamper-evident chain columns of the DB copy of the audit trail.
- Index/constraint/server-default definitions differed from the models, which
  keeps `alembic check` (the CI drift gate) permanently red.

Every step is guarded with an inspector so the migration is idempotent and
runs against both schema lineages:

- a 001→004 migration-built database, where it does all of the above;
- a database the create_all backstop built (brownfield installs are stamped at
  004 by app/utils/migration_bootstrap.py and then run 005+), which already
  has the columns, indexes and client-side defaults — and, since 1.2.0, no
  longer has the document-assembly tables (templates, clauses, court_rules,
  generated_documents, ...) whose defaults and constraints are altered here.
  Unguarded ALTERs on those missing tables crash-looped the container.

The server-default change runs through batch_alter_table: a plain ALTER on
PostgreSQL (the only backend start.sh migrates), a table rebuild on SQLite,
which is what lets the test suite exercise this file.

Revision ID: 005
Revises: 004
Create Date: 2026-07-13
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "005"
down_revision: str = "004"
branch_labels = None
depends_on = None

# (table, column) pairs whose server_default=now() from 001 the models do not
# declare (they use client-side defaults).
_SERVER_DEFAULT_COLUMNS = [
    ("audit_logs", "timestamp"),
    ("clause_variants", "created_at"),
    ("clauses", "created_at"),
    ("court_rules", "created_at"),
    ("generated_documents", "created_at"),
    ("template_versions", "created_at"),
    ("templates", "created_at"),
    ("users", "created_at"),
]


def _existing_tables() -> set[str]:
    return set(sa.inspect(op.get_bind()).get_table_names())


def _existing_columns(table: str) -> set[str]:
    inspector = sa.inspect(op.get_bind())
    return {col["name"] for col in inspector.get_columns(table)}


def _existing_indexes(table: str) -> set[str]:
    inspector = sa.inspect(op.get_bind())
    return {ix["name"] for ix in inspector.get_indexes(table)}


def _existing_uniques(table: str) -> set[str]:
    inspector = sa.inspect(op.get_bind())
    return {uc["name"] for uc in inspector.get_unique_constraints(table)}


def _existing_foreign_keys(table: str) -> set[str]:
    inspector = sa.inspect(op.get_bind())
    return {fk["name"] for fk in inspector.get_foreign_keys(table) if fk["name"]}


def _has_server_default(table: str, column: str) -> bool:
    inspector = sa.inspect(op.get_bind())
    for col in inspector.get_columns(table):
        if col["name"] == column:
            return col.get("default") is not None
    return False


def _add_column_if_missing(table: str, column: sa.Column) -> None:
    if column.name not in _existing_columns(table):
        op.add_column(table, column)


def _set_server_default(table: str, column: str, server_default) -> None:
    """Set (or clear, with ``None``) a column's server default if it differs.

    Skipped when the table is gone (removed in 1.2.0) or the column already has
    the requested state, so re-runs and create_all-built schemas are no-ops.
    """
    if table not in _existing_tables():
        return
    if _has_server_default(table, column) == (server_default is not None):
        return
    with op.batch_alter_table(table) as batch:
        batch.alter_column(
            column,
            existing_type=sa.TIMESTAMP(),
            server_default=server_default,
            existing_nullable=False,
        )


def upgrade() -> None:
    # --- Missing columns (functional fixes) ---
    _add_column_if_missing("audit_logs", sa.Column("entry_hash", sa.String(length=64)))
    _add_column_if_missing("audit_logs", sa.Column("previous_hash", sa.String(length=64)))
    _add_column_if_missing("audit_logs", sa.Column("correlation_id", sa.String(length=36)))
    _add_column_if_missing("users", sa.Column("mfa_secret", sa.String(length=255)))
    _add_column_if_missing("users", sa.Column("mfa_recovery_codes", sa.JSON()))
    _add_column_if_missing("users", sa.Column("password_changed_at", sa.DateTime()))

    # --- Server-default alignment (models use client-side defaults) ---
    for table, column in _SERVER_DEFAULT_COLUMNS:
        _set_server_default(table, column, None)

    # --- Index / constraint alignment with the model definitions ---
    audit_indexes = _existing_indexes("audit_logs")
    if "idx_audit_event_type" in audit_indexes:
        op.drop_index("idx_audit_event_type", table_name="audit_logs")
    if "idx_audit_timestamp" in audit_indexes:
        op.drop_index("idx_audit_timestamp", table_name="audit_logs")
    if "idx_audit_resource" not in audit_indexes:
        op.create_index("idx_audit_resource", "audit_logs", ["resource_type", "resource_id"])
    if "ix_audit_logs_event_type" not in audit_indexes:
        op.create_index("ix_audit_logs_event_type", "audit_logs", ["event_type"])
    if "ix_audit_logs_timestamp" not in audit_indexes:
        op.create_index("ix_audit_logs_timestamp", "audit_logs", ["timestamp"])

    # generated_documents.parent_id FK gains ON DELETE SET NULL (matches model).
    # The table is gone on 1.2.0+ create_all schemas; the named constraint only
    # exists where 001 built it (PostgreSQL auto-names FKs, SQLite does not).
    if "generated_documents" in _existing_tables() and (
        "generated_documents_parent_id_fkey" in _existing_foreign_keys("generated_documents")
    ):
        op.drop_constraint(
            "generated_documents_parent_id_fkey", "generated_documents", type_="foreignkey"
        )
        op.create_foreign_key(
            "generated_documents_parent_id_fkey",
            "generated_documents",
            "generated_documents",
            ["parent_id"],
            ["id"],
            ondelete="SET NULL",
        )

    # users: plain indexes + table-level uniques become the models' unique indexes.
    user_indexes = _existing_indexes("users")
    if "idx_user_azure_oid" in user_indexes:
        op.drop_index("idx_user_azure_oid", table_name="users")
    if "idx_user_email" in user_indexes:
        op.drop_index("idx_user_email", table_name="users")
    user_uniques = _existing_uniques("users")
    if "users_azure_oid_key" in user_uniques:
        op.drop_constraint("users_azure_oid_key", "users", type_="unique")
    if "users_email_key" in user_uniques:
        op.drop_constraint("users_email_key", "users", type_="unique")
    user_indexes = _existing_indexes("users")
    if "ix_users_azure_oid" not in user_indexes:
        op.create_index("ix_users_azure_oid", "users", ["azure_oid"], unique=True)
    if "ix_users_email" not in user_indexes:
        op.create_index("ix_users_email", "users", ["email"], unique=True)


def downgrade() -> None:
    user_indexes = _existing_indexes("users")
    if "ix_users_email" in user_indexes:
        op.drop_index("ix_users_email", table_name="users")
    if "ix_users_azure_oid" in user_indexes:
        op.drop_index("ix_users_azure_oid", table_name="users")
    user_uniques = _existing_uniques("users")
    if "users_email_key" not in user_uniques:
        op.create_unique_constraint("users_email_key", "users", ["email"])
    if "users_azure_oid_key" not in user_uniques:
        op.create_unique_constraint("users_azure_oid_key", "users", ["azure_oid"])
    user_indexes = _existing_indexes("users")
    if "idx_user_email" not in user_indexes:
        op.create_index("idx_user_email", "users", ["email"])
    if "idx_user_azure_oid" not in user_indexes:
        op.create_index("idx_user_azure_oid", "users", ["azure_oid"])

    if "generated_documents" in _existing_tables() and (
        "generated_documents_parent_id_fkey" in _existing_foreign_keys("generated_documents")
    ):
        op.drop_constraint(
            "generated_documents_parent_id_fkey", "generated_documents", type_="foreignkey"
        )
        op.create_foreign_key(
            "generated_documents_parent_id_fkey",
            "generated_documents",
            "generated_documents",
            ["parent_id"],
            ["id"],
        )

    audit_indexes = _existing_indexes("audit_logs")
    if "ix_audit_logs_timestamp" in audit_indexes:
        op.drop_index("ix_audit_logs_timestamp", table_name="audit_logs")
    if "ix_audit_logs_event_type" in audit_indexes:
        op.drop_index("ix_audit_logs_event_type", table_name="audit_logs")
    if "idx_audit_resource" in audit_indexes:
        op.drop_index("idx_audit_resource", table_name="audit_logs")
    if "idx_audit_timestamp" not in audit_indexes:
        op.create_index("idx_audit_timestamp", "audit_logs", ["timestamp"])
    if "idx_audit_event_type" not in audit_indexes:
        op.create_index("idx_audit_event_type", "audit_logs", ["event_type"])

    for table, column in _SERVER_DEFAULT_COLUMNS:
        _set_server_default(table, column, sa.text("now()"))

    user_columns = _existing_columns("users")
    for column in ("password_changed_at", "mfa_recovery_codes", "mfa_secret"):
        if column in user_columns:
            op.drop_column("users", column)
    audit_columns = _existing_columns("audit_logs")
    for column in ("correlation_id", "previous_hash", "entry_hash"):
        if column in audit_columns:
            op.drop_column("audit_logs", column)
