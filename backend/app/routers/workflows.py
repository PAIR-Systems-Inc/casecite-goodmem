"""Workflow endpoints: define a repeatable sequence of steps, run it over a set
of documents as one background job, read and resume past runs.

Access follows the other work-product routers: the owner or a member of the
workflow's matter may read it and run it; the owner alone edits the steps or
deletes it. Anyone else gets 404, never 403. Every input document must be
readable by the runner at that moment, so a colleague's private document
cannot be put through a shared workflow. Permission and document checks
happen here, once; the steps then call the service layer directly.
"""

import logging
from typing import Any

from fastapi import APIRouter, Depends, Query, Request
from fastapi.responses import JSONResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db
from app.exceptions import BadRequestError, NotFoundError, UnprocessableError
from app.models.schemas import (
    WorkflowCreateRequest,
    WorkflowDetailResponse,
    WorkflowListResponse,
    WorkflowRunDetailResponse,
    WorkflowRunListResponse,
    WorkflowRunRequest,
    WorkflowRunStarted,
    WorkflowRunSummary,
    WorkflowStep,
    WorkflowStepRunResponse,
    WorkflowSummary,
    WorkflowTemplate,
    WorkflowTemplatesResponse,
    WorkflowUpdateRequest,
)
from app.models.workflows import RUN_RUNNING, Workflow, WorkflowRun, WorkflowStepRun
from app.routers._matter_deps import accessible_matter_ids
from app.services import matters as matters_service
from app.services.audit import AuditEventType, audit_service
from app.services.auth import TokenData
from app.services.documents import document_service
from app.services.job_queue import RetryPolicy, job_manager
from app.services.permissions import require_permission
from app.services.user_keys import UserAPIKeys
from app.services.workflows import WorkflowError, workflow_service
from app.services.workflows.templates import TEMPLATES, template_by_key, template_steps
from app.utils.ip_resolution import get_client_ip

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/workflows", tags=["workflows"])


async def _audit(
    request: Request,
    current_user: TokenData,
    event_type: AuditEventType,
    action: str,
    workflow_id: str,
    **details: Any,
) -> None:
    """Ids and counts only — never step parameters, document text or outputs."""
    await audit_service.log_event(
        event_type=event_type,
        user_id=current_user.user_id,
        user_email=current_user.email,
        resource_type="workflow",
        resource_id=workflow_id,
        ip_address=get_client_ip(request),
        details={"action": action, **details},
    )


async def _require_readable(
    db: AsyncSession, workflow_id: str, current_user: TokenData, matter_ids: set[str]
) -> Workflow:
    workflow = await workflow_service.get_accessible(
        db, workflow_id, current_user.user_id, matter_ids
    )
    if workflow is None:
        raise NotFoundError("Workflow not found")
    return workflow


async def _require_owned(
    db: AsyncSession, workflow_id: str, current_user: TokenData, matter_ids: set[str]
) -> Workflow:
    workflow = await _require_readable(db, workflow_id, current_user, matter_ids)
    if workflow.owner_id != current_user.user_id:
        raise NotFoundError("Workflow not found")
    return workflow


async def _require_run(db: AsyncSession, workflow: Workflow, run_id: str) -> WorkflowRun:
    run = await workflow_service.get_run(db, workflow.id, run_id)
    if run is None:
        raise NotFoundError("Workflow run not found")
    return run


async def _check_documents(ids: list[str], current_user: TokenData, matter_ids: set[str]) -> None:
    """Every document a run reads must be readable by the runner right now."""
    for doc_id in dict.fromkeys(ids):
        if await document_service.get_document(doc_id, current_user.user_id, matter_ids) is None:
            raise NotFoundError("Document not found")


def _fixed_document_ids(steps: list[dict[str, Any]]) -> list[str]:
    """Document ids a workflow names in its steps (explicit inputs, baselines, references)."""
    ids: list[str] = []
    for step in steps:
        ids.extend((step.get("inputs") or {}).get("document_ids") or [])
        params = step.get("params") or {}
        if params.get("baseline_document_id"):
            ids.append(str(params["baseline_document_id"]))
        ids.extend(params.get("reference_document_ids") or [])
    return list(dict.fromkeys(ids))


def _check_step_count(steps: list[Any]) -> None:
    cap = workflow_service.caps()["max_steps"]
    if len(steps) > cap:
        raise UnprocessableError(f"A workflow may have at most {cap} steps on this server.")


async def _default_scope(db: AsyncSession, matter_id: str) -> list[str]:
    matter = await matters_service.get_matter(db, matter_id)
    if matter is None:
        raise NotFoundError("Matter not found")
    personal_owner = str(matter.owner_id) if matter.is_personal else None
    return [d.id for d in document_service.documents_in_matter(matter_id, personal_owner)]


def _summary(workflow: Workflow, caller_id: str, stats: dict[str, Any] | None) -> WorkflowSummary:
    stats = stats or {}
    return WorkflowSummary(
        id=workflow.id,
        name=workflow.name,
        description=workflow.description,
        matter_id=workflow.matter_id,
        owner_id=workflow.owner_id,
        role=workflow_service.role_for(workflow, caller_id),
        template_key=workflow.template_key,
        step_count=len(workflow.steps or []),
        step_kinds=[s["kind"] for s in (workflow.steps or [])],
        run_count=int(stats.get("run_count") or 0),
        last_run_at=stats.get("last_run_at") or workflow.last_run_at,
        last_run_status=stats.get("last_run_status"),
        created_at=workflow.created_at,
        updated_at=workflow.updated_at,
    )


async def _detail(db: AsyncSession, workflow: Workflow, caller_id: str) -> WorkflowDetailResponse:
    stats = await workflow_service.run_stats(db, [workflow.id])
    return WorkflowDetailResponse(
        **_summary(workflow, caller_id, stats.get(workflow.id)).model_dump(),
        steps=[WorkflowStep(**s) for s in (workflow.steps or [])],
    )


def _step_run_response(row: WorkflowStepRun) -> WorkflowStepRunResponse:
    return WorkflowStepRunResponse(
        index=row.index,
        name=row.name,
        kind=row.kind,
        status=row.status,
        output=row.output,
        input_document_ids=list(row.input_document_ids or []),
        produced_ids=dict(row.produced_ids or {}),
        error=row.error,
        started_at=row.started_at,
        finished_at=row.finished_at,
    )


def _run_summary(run: WorkflowRun, completed: int) -> WorkflowRunSummary:
    return WorkflowRunSummary(
        id=run.id,
        workflow_id=run.workflow_id,
        runner_id=run.runner_id,
        matter_id=run.matter_id,
        status=run.status,
        steps=len(run.steps or []),
        completed_steps=completed,
        documents=len(run.input_document_ids or []),
        job_id=run.job_id,
        error=run.error,
        resumed_count=int(run.resumed_count or 0),
        created_at=run.created_at,
        completed_at=run.completed_at,
    )


async def _run_detail(db: AsyncSession, run: WorkflowRun) -> WorkflowRunDetailResponse:
    rows = await workflow_service.step_runs(db, run.id)
    completed = sum(1 for r in rows if r.status == "completed")
    return WorkflowRunDetailResponse(
        **_run_summary(run, completed).model_dump(),
        step_definitions=[WorkflowStep(**s) for s in (run.steps or [])],
        step_runs=[_step_run_response(r) for r in rows],
        input_document_ids=list(run.input_document_ids or []),
    )


def _require_client(request: Request, current_user: TokenData) -> UserAPIKeys:
    """The runner's keys, refused now (400) rather than inside the job when no model is set."""
    user_keys = UserAPIKeys.from_request(request, user_id=current_user.user_id)
    try:
        workflow_service._llm_client(user_keys)
    except ValueError as e:
        raise BadRequestError(str(e))
    return user_keys


async def _submit(
    request: Request,
    db: AsyncSession,
    current_user: TokenData,
    matter_ids: set[str],
    workflow: Workflow,
    run: WorkflowRun,
    *,
    user_keys: UserAPIKeys,
    start_index: int,
    action: str,
) -> JSONResponse:
    """Submit the run (or its resumption) as one job; 202 + a job to poll."""
    user_id, user_email = current_user.user_id, current_user.email
    steps = len(run.steps or [])
    documents = len(run.input_document_ids or [])
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_ACCESS,
        action,
        workflow.id,
        run_id=run.id,
        steps=steps,
        start_index=start_index,
        documents=documents,
        step_kinds=[s["kind"] for s in (run.steps or [])],
    )

    async def do_work() -> dict[str, Any]:
        try:
            result = await workflow_service.execute(
                run_id=run.id,
                user_id=user_id,
                accessible_matter_ids=matter_ids,
                user_keys=user_keys,
                start_index=start_index,
            )
        except WorkflowError as e:
            await audit_service.log_event(
                event_type=AuditEventType.DATA_ACCESS,
                user_id=user_id,
                user_email=user_email,
                resource_type="workflow",
                resource_id=workflow.id,
                details={"action": "workflow_run_failed", "run_id": run.id, "error": str(e)[:200]},
            )
            raise
        await audit_service.log_event(
            event_type=AuditEventType.DATA_ACCESS,
            user_id=user_id,
            user_email=user_email,
            resource_type="workflow",
            resource_id=workflow.id,
            details={"action": "workflow_run_completed", **result},
        )
        return result

    # Steps are expensive and already persist their own outcome; a failed step
    # is resumed deliberately, never re-run by the queue.
    job_id = await job_manager.submit(
        do_work,
        user_id=user_id,
        endpoint=f"/workflows/{workflow.id}/run",
        retry_policy=RetryPolicy(max_retries=0),
    )
    await workflow_service.set_job(db, run.id, job_id)
    payload = WorkflowRunStarted(
        run_id=run.id,
        job_id=job_id,
        poll_url=f"/api/v1/jobs/{job_id}",
        steps=steps,
        documents=documents,
    )
    return JSONResponse(status_code=202, content=payload.model_dump())


# ------------------------------------------------------------------- collection


@router.get("", response_model=WorkflowListResponse)
async def list_workflows(
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowListResponse:
    """Workflows the caller owns or can read through a matter, newest first."""
    workflows = await workflow_service.list_for_user(db, current_user.user_id, matter_ids)
    stats = await workflow_service.run_stats(db, [w.id for w in workflows])
    return WorkflowListResponse(
        workflows=[_summary(w, current_user.user_id, stats.get(w.id)) for w in workflows]
    )


@router.post("", response_model=WorkflowDetailResponse, status_code=201)
async def create_workflow(
    body: WorkflowCreateRequest,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowDetailResponse:
    if body.matter_id and body.matter_id not in matter_ids:
        raise NotFoundError("Matter not found")
    _check_step_count(body.steps)
    steps = [s.model_dump(mode="json") for s in body.steps]
    await _check_documents(_fixed_document_ids(steps), current_user, matter_ids)
    workflow = await workflow_service.create(
        db,
        owner_id=current_user.user_id,
        matter_id=body.matter_id,
        name=body.name,
        description=body.description,
        steps=steps,
    )
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_MODIFICATION,
        "workflow_created",
        workflow.id,
        matter_id=body.matter_id,
        steps=len(steps),
        step_kinds=[s["kind"] for s in steps],
    )
    return await _detail(db, workflow, current_user.user_id)


@router.get("/templates", response_model=WorkflowTemplatesResponse)
async def list_templates(
    current_user: TokenData = require_permission("contracts.use"),
) -> WorkflowTemplatesResponse:
    """The built-in starter workflows. Instantiate one with POST /workflows/from-template/{key}."""
    return WorkflowTemplatesResponse(
        templates=[
            WorkflowTemplate(
                key=t["key"],
                name=t["name"],
                description=t["description"],
                steps=template_steps(t),
            )
            for t in TEMPLATES
        ]
    )


@router.post("/from-template/{key}", response_model=WorkflowDetailResponse, status_code=201)
async def create_from_template(
    key: str,
    request: Request,
    matter_id: str | None = Query(None, max_length=36),
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowDetailResponse:
    """A workflow of the caller's own, copied from a template; edit it freely afterwards."""
    template = template_by_key(key)
    if template is None:
        raise NotFoundError("Template not found")
    if matter_id and matter_id not in matter_ids:
        raise NotFoundError("Matter not found")
    steps = [s.model_dump(mode="json") for s in template_steps(template)]
    workflow = await workflow_service.create(
        db,
        owner_id=current_user.user_id,
        matter_id=matter_id,
        name=template["name"],
        description=template["description"],
        steps=steps,
        template_key=key,
    )
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_MODIFICATION,
        "workflow_created",
        workflow.id,
        matter_id=matter_id,
        template=key,
        steps=len(steps),
        step_kinds=[s["kind"] for s in steps],
    )
    return await _detail(db, workflow, current_user.user_id)


# ------------------------------------------------------------------ one workflow


@router.get("/{workflow_id}", response_model=WorkflowDetailResponse)
async def get_workflow(
    workflow_id: str,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowDetailResponse:
    workflow = await _require_readable(db, workflow_id, current_user, matter_ids)
    return await _detail(db, workflow, current_user.user_id)


@router.patch("/{workflow_id}", response_model=WorkflowDetailResponse)
async def update_workflow(
    workflow_id: str,
    body: WorkflowUpdateRequest,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowDetailResponse:
    """Owner only. Past runs keep the steps they ran with."""
    workflow = await _require_owned(db, workflow_id, current_user, matter_ids)
    if body.model_dump(exclude_unset=True) == {}:
        raise BadRequestError("Nothing to update.")
    steps = None
    if body.steps is not None:
        _check_step_count(body.steps)
        steps = [s.model_dump(mode="json") for s in body.steps]
        await _check_documents(_fixed_document_ids(steps), current_user, matter_ids)
    workflow = await workflow_service.update(
        db, workflow, name=body.name, description=body.description, steps=steps
    )
    await _audit(
        request,
        current_user,
        AuditEventType.DATA_MODIFICATION,
        "workflow_updated",
        workflow.id,
        changed=sorted(body.model_dump(exclude_unset=True)),
        steps=len(workflow.steps or []),
    )
    return await _detail(db, workflow, current_user.user_id)


@router.delete("/{workflow_id}")
async def delete_workflow(
    workflow_id: str,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> dict[str, str]:
    """Owner only; the runs go with it. Work product the runs created
    (review tables, analyses, research runs) stays."""
    workflow = await _require_owned(db, workflow_id, current_user, matter_ids)
    await workflow_service.delete(db, workflow)
    await _audit(
        request, current_user, AuditEventType.DATA_DELETION, "workflow_deleted", workflow_id
    )
    return {"status": "deleted", "id": workflow_id}


# ------------------------------------------------------------------------- runs


@router.post("/{workflow_id}/run", response_model=WorkflowRunStarted, status_code=202)
async def run_workflow(
    workflow_id: str,
    request: Request,
    body: WorkflowRunRequest | None = None,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> JSONResponse:
    """Run the workflow over the given documents as one job (202 + a job to poll).

    With no ids and a workflow filed under a matter, every document filed
    under that matter is the input. Owner or matter member may run; every
    input must be readable by the runner.
    """
    workflow = await _require_readable(db, workflow_id, current_user, matter_ids)
    if not workflow.steps:
        raise BadRequestError("Add at least one step before running.")
    body = body or WorkflowRunRequest()
    document_ids = list(dict.fromkeys(body.document_ids))
    if not document_ids and workflow.matter_id:
        document_ids = await _default_scope(db, workflow.matter_id)
    if not document_ids:
        raise BadRequestError("Pick at least one document to run the workflow on.")
    await _check_documents(document_ids, current_user, matter_ids)
    await _check_documents(_fixed_document_ids(list(workflow.steps)), current_user, matter_ids)
    user_keys = _require_client(request, current_user)

    run = await workflow_service.create_run(
        db, workflow, runner_id=current_user.user_id, document_ids=document_ids
    )
    return await _submit(
        request,
        db,
        current_user,
        matter_ids,
        workflow,
        run,
        user_keys=user_keys,
        start_index=0,
        action="workflow_run_started",
    )


@router.get("/{workflow_id}/runs", response_model=WorkflowRunListResponse)
async def list_runs(
    workflow_id: str,
    limit: int = Query(50, ge=1, le=200),
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowRunListResponse:
    workflow = await _require_readable(db, workflow_id, current_user, matter_ids)
    runs = await workflow_service.list_runs(db, workflow.id, limit=limit)
    completed = await workflow_service.completed_counts(db, [r.id for r in runs])
    return WorkflowRunListResponse(runs=[_run_summary(r, completed.get(r.id, 0)) for r in runs])


@router.get("/{workflow_id}/runs/{run_id}", response_model=WorkflowRunDetailResponse)
async def get_run(
    workflow_id: str,
    run_id: str,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> WorkflowRunDetailResponse:
    workflow = await _require_readable(db, workflow_id, current_user, matter_ids)
    run = await _require_run(db, workflow, run_id)
    return await _run_detail(db, run)


@router.post(
    "/{workflow_id}/runs/{run_id}/resume", response_model=WorkflowRunStarted, status_code=202
)
async def resume_run(
    workflow_id: str,
    run_id: str,
    request: Request,
    current_user: TokenData = require_permission("contracts.use"),
    db: AsyncSession = Depends(get_db),
    matter_ids: set[str] = Depends(accessible_matter_ids),
) -> JSONResponse:
    """Re-submit a failed or cancelled run from its first unfinished step, with
    every earlier step's output reloaded. The steps run are the ones the run
    started with, even if the workflow has been edited since."""
    workflow = await _require_readable(db, workflow_id, current_user, matter_ids)
    run = await _require_run(db, workflow, run_id)
    if run.status == RUN_RUNNING:
        raise BadRequestError("This run is still in progress.")
    await _check_documents(list(run.input_document_ids or []), current_user, matter_ids)
    user_keys = _require_client(request, current_user)
    try:
        start_index = await workflow_service.prepare_resume(db, run)
    except ValueError as e:
        raise BadRequestError(str(e))
    return await _submit(
        request,
        db,
        current_user,
        matter_ids,
        workflow,
        run,
        user_keys=user_keys,
        start_index=start_index,
        action="workflow_run_resumed",
    )
