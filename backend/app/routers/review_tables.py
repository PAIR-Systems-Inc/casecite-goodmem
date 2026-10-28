"""Tabular review endpoints: define a field schema, point it at many documents,
run quote-verified extraction as one background job, export cited results.

Access follows the other work-product routers: the owner or a member of the
table's matter may read, run and export; the owner alone edits the schema or
scope and deletes. Anyone else gets 404, never 403. Documents put in scope
must be readable by the caller at that moment (owner or matter member), so a
colleague's private document cannot be reviewed through a shared table.
"""

import logging
import re
from typing import Any

from fastapi import APIRouter, Depends, Query, Request
from fastapi.responses import JSONResponse, Response
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.exceptions import BadRequestError, NotFoundError
from app.models.review_tables import ReviewCell, ReviewTable
from app.models.schemas import (
    Document,
    ReviewCellResponse,
    ReviewFieldSpec,
    ReviewTableCreateRequest,
    ReviewTableDetailResponse,
    ReviewTableDocument,
    ReviewTableEstimateResponse,
    ReviewTableListResponse,
    ReviewTableRunRequest,
    ReviewTableRunStarted,
    ReviewTableSummary,
    ReviewTableUpdateRequest,
)
from app.routers._matter_deps import accessible_matter_ids
from app.services import matters as matters_service
from app.services.audit import AuditEventType, audit_service
from app.services.auth import TokenData
from app.services.documents import document_service
from app.services.job_queue import RETRY_POLICIES, job_manager
from app.services.permissions import require_permission
from app.services.review_tables import review_table_service
from app.services.review_tables.export import render_csv, render_xlsx
from app.services.user_keys import UserAPIKeys
from app.utils.ip_resolution import get_client_ip

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/review-tables", tags=["review-tables"])

_XLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"


async def _audit(
    request: Request,
    current_user: TokenData,
    event_type: AuditEventType,
    action: str,
    table_id: str,
    **details: Any,
) -> None:
    """Audit a review-table action. Ids and counts only — never field values or quotes."""
    await audit_service.log_event(
        event_type=event_type,
        user_id=current_user.user_id,
        user_email=current_user.email,
        resource_type="review_table",
        resource_id=table_id,
        ip_address=get_client_ip(request),
        details={"action": action, **details},
    )


async def _require_readable(
    db: AsyncSession, table_id: str, current_user: TokenData, matter_ids: set[str]
) -> ReviewTable:
    table = await review_table_service.get_accessible(
        db, table_id, current_user.user_id, matter_ids
    )
    if table is None:
        raise NotFoundError("Review table not found")
    return table


async def _require_owned(
    db: AsyncSession, table_id: str, current_user: TokenData, matter_ids: set[str]
) -> ReviewTable:
    table = await _require_readable(db, table_id, current_user, matter_ids)
    if table.owner_id != current_user.user_id:
        raise NotFoundError("Review table not found")
    return table


async def _check_documents(ids: list[str], current_user: TokenData, matter_ids: set[str]) -> None:
    """Every document put in scope must be readable by the caller right now."""
    for doc_id in dict.fromkeys(ids):
        if await document_service.get_document(doc_id, current_user.user_id, matter_ids) is None:
            raise NotFoundError("Document not found")


async def _default_scope(db: AsyncSession, matter_id: str, current_user: TokenData) -> list[str]:
    """Every document filed under the matter, for a table created without ids."""
    matter = await matters_service.get_matter(db, matter_id)
    if matter is None:
        raise NotFoundError("Matter not found")
    personal_owner = str(matter.owner_id) if matter.is_personal else None
    return [d.id for d in document_service.documents_in_matter(matter_id, personal_owner)]


def _summary(table: ReviewTable, caller_id: str, cell_count: int) -> ReviewTableSummary:
    return ReviewTableSummary(
        id=table.id,
        name=table.name,
        description=table.description,
        matter_id=table.matter_id,
        owner_id=table.owner_id,
        role=review_table_service.role_for(table, caller_id),
        field_count=len(table.fields or []),
        document_count=len(table.document_ids or []),
        cell_count=cell_count,
        last_run_at=table.last_run_at,
        created_at=table.created_at,
        updated_at=table.updated_at,
    )


def _cell_response(cell: ReviewCell) -> ReviewCellResponse:
    return ReviewCellResponse(
        document_id=cell.document_id,
        field_key=cell.field_key,
        value=cell.value,
        quote=cell.quote,
        span_start=cell.span_start,
        span_end=cell.span_end,
        verified=bool(cell.verified),
        status=cell.status,
        error=cell.error,
        model_used=cell.model_used,
        updated_at=cell.updated_at,
    )


def _document_entries(resolved: list[tuple[str, Document | None]]) -> list[ReviewTableDocument]:
    return [
        ReviewTableDocument(
            id=doc_id,
            filename=doc.filename if doc else "Unavailable document",
            accessible=doc is not None,
        )
        for doc_id, doc in resolved
    ]


async def _detail(
    db: AsyncSession, table: ReviewTable, current_user: TokenData, matter_ids: set[str]
) -> ReviewTableDetailResponse:
    cells = await review_table_service.cells(db, table.id)
    resolved = await review_table_service.resolve_documents(table, current_user.user_id, matter_ids)
    return ReviewTableDetailResponse(
        **_summary(table, current_user.user_id, len(cells)).model_dump(),
        fields=[ReviewFieldSpec(**f) for f in (table.fields or [])],
        documents=_document_entries(resolved),
        cells=[_cell_response(c) for c in cells],
    )


# ------------------------------------------------------------------- collection


@router.get("", response_model=ReviewTableListResponse)
async def list_review_tables(
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> ReviewTableListResponse:
    """Tables the caller owns or can read through a matter, newest first."""
    tables = await review_table_service.list_for_user(db, current_user.user_id, matter_ids)
    counts = await review_table_service.cell_counts(db, [t.id for t in tables])
    return ReviewTableListResponse(
        tables=[_summary(t, current_user.user_id, counts.get(t.id, 0)) for t in tables]
    )


@router.post("", response_model=ReviewTableDetailResponse, status_code=201)
async def create_review_table(
    body: ReviewTableCreateRequest,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> ReviewTableDetailResponse:
    """Create a table. With a matter and no document ids, the scope is the matter's documents."""
    if body.matter_id and body.matter_id not in matter_ids:
        raise NotFoundError("Matter not found")
    document_ids = list(body.document_ids)
    if not document_ids and body.matter_id:
        document_ids = await _default_scope(db, body.matter_id, current_user)
    await _check_documents(document_ids, current_user, matter_ids)
    table = await review_table_service.create(
        db,
        owner_id=current_user.user_id,
        matter_id=body.matter_id,
        name=body.name,
        description=body.description,
        fields=[f.model_dump() for f in body.fields],
        document_ids=document_ids,
    )
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_MODIFICATION,
        "review_table_created",
        table.id,
        matter_id=body.matter_id,
        fields=len(body.fields),
        documents=len(document_ids),
    )
    return await _detail(db, table, current_user, matter_ids)


# ------------------------------------------------------------------ one table


@router.get("/{table_id}", response_model=ReviewTableDetailResponse)
async def get_review_table(
    table_id: str,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> ReviewTableDetailResponse:
    table = await _require_readable(db, table_id, current_user, matter_ids)
    return await _detail(db, table, current_user, matter_ids)


@router.patch("/{table_id}", response_model=ReviewTableDetailResponse)
async def update_review_table(
    table_id: str,
    body: ReviewTableUpdateRequest,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> ReviewTableDetailResponse:
    """Owner only. Dropping a field or a document also drops its cells."""
    table = await _require_owned(db, table_id, current_user, matter_ids)
    if body.model_dump(exclude_unset=True) == {}:
        raise BadRequestError("Nothing to update.")
    if body.document_ids is not None:
        await _check_documents(body.document_ids, current_user, matter_ids)
    table = await review_table_service.update(
        db,
        table,
        name=body.name,
        description=body.description,
        fields=[f.model_dump() for f in body.fields] if body.fields is not None else None,
        document_ids=body.document_ids,
    )
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_MODIFICATION,
        "review_table_updated",
        table.id,
        changed=sorted(body.model_dump(exclude_unset=True)),
    )
    return await _detail(db, table, current_user, matter_ids)


@router.delete("/{table_id}")
async def delete_review_table(
    table_id: str,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> dict[str, str]:
    table = await _require_owned(db, table_id, current_user, matter_ids)
    await review_table_service.delete(db, table)
    await _audit(
        request, current_user, AuditEventType.DATA_DELETION, "review_table_deleted", table_id
    )
    return {"status": "deleted", "id": table_id}


@router.get("/{table_id}/estimate", response_model=ReviewTableEstimateResponse)
async def estimate_review_table(
    table_id: str,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> ReviewTableEstimateResponse:
    """Documents × fields and the approximate prompt tokens a full run would send."""
    table = await _require_readable(db, table_id, current_user, matter_ids)
    resolved = await review_table_service.resolve_documents(table, current_user.user_id, matter_ids)
    estimate = review_table_service.estimate(
        [doc for _id, doc in resolved], len(table.fields or [])
    )
    return ReviewTableEstimateResponse(**estimate)


@router.post("/{table_id}/run", response_model=ReviewTableRunStarted, status_code=202)
async def run_review_table(
    table_id: str,
    request: Request,
    body: ReviewTableRunRequest | None = None,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> JSONResponse:
    """Start extraction as one job (202 + a job to poll). Optional ids and
    field keys narrow it to a re-run of some rows or columns."""
    table = await _require_readable(db, table_id, current_user, matter_ids)
    body = body or ReviewTableRunRequest()
    scope_docs = set(table.document_ids or [])
    scope_keys = {f["key"] for f in (table.fields or [])}
    document_ids = [d for d in dict.fromkeys(body.document_ids or []) if d in scope_docs] or None
    field_keys = [k for k in dict.fromkeys(body.field_keys or []) if k in scope_keys] or None
    if body.document_ids and not document_ids:
        raise BadRequestError("None of the given documents is in this table.")
    if body.field_keys and not field_keys:
        raise BadRequestError("None of the given fields is in this table.")
    if not scope_docs or not scope_keys:
        raise BadRequestError("Add at least one document and one field before running.")

    user_keys = UserAPIKeys.from_request(request, user_id=current_user.user_id)
    try:
        review_table_service._llm_client(user_keys)  # fail now, not inside the job
    except ValueError as e:
        raise BadRequestError(str(e))

    user_id, user_email = current_user.user_id, current_user.email
    documents = len(document_ids or scope_docs)
    fields = len(field_keys or scope_keys)
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_ACCESS,
        "review_table_run_started",
        table_id,
        documents=documents,
        fields=fields,
    )

    async def do_work() -> dict[str, Any]:
        result = await review_table_service.run(
            table_id=table_id,
            user_id=user_id,
            accessible_matter_ids=matter_ids,
            user_keys=user_keys,
            document_ids=document_ids,
            field_keys=field_keys,
        )
        await audit_service.log_event(
            event_type=AuditEventType.DATA_ACCESS,
            user_id=user_id,
            user_email=user_email,
            resource_type="review_table",
            resource_id=table_id,
            details={"action": "review_table_run_completed", **result},
        )
        return result

    job_id = await job_manager.submit(
        do_work,
        user_id=user_id,
        endpoint=f"/review-tables/{table_id}/run",
        retry_policy=RETRY_POLICIES["ai_analysis"],
    )
    payload = ReviewTableRunStarted(
        job_id=job_id, poll_url=f"/api/v1/jobs/{job_id}", documents=documents, fields=fields
    )
    return JSONResponse(status_code=202, content=payload.model_dump())


@router.get("/{table_id}/export")
async def export_review_table(
    table_id: str,
    request: Request,
    format: str = Query("xlsx", pattern="^(xlsx|csv)$"),
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> Response:
    """Results with citations, as a workbook (Results + Citations sheets) or a flat CSV."""
    table = await _require_readable(db, table_id, current_user, matter_ids)
    cells = [_cell_response(c).model_dump() for c in await review_table_service.cells(db, table.id)]
    resolved = await review_table_service.resolve_documents(table, current_user.user_id, matter_ids)
    documents = [d.model_dump() for d in _document_entries(resolved)]
    fields = list(table.fields or [])
    stem = re.sub(r"[^A-Za-z0-9._-]+", "-", table.name).strip("-")[:60] or "review"
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_EXPORT,
        "review_table_export",
        table_id,
        format=format,
        cells=len(cells),
    )
    if format == "csv":
        return Response(
            content=render_csv(fields, documents, cells),
            media_type="text/csv",
            headers={"Content-Disposition": f'attachment; filename="{stem}.csv"'},
        )
    return Response(
        content=render_xlsx(table.name, fields, documents, cells),
        media_type=_XLSX,
        headers={"Content-Disposition": f'attachment; filename="{stem}.xlsx"'},
    )
