"""Workflows: persist them, start runs, execute the steps in order.

A run is one background job. Steps execute one after another; each step's
status, output and error are written as they happen, so a run that stops can
be read in full and resumed from the step that failed with every earlier
output reloaded. Permission checks happen once, at the top of the run, in the
router; every step then reads documents as the runner (owner or matter
member) and calls the service layer directly.

Access follows the other work-product services: the owner, or a member of the
workflow's matter, may read and run it; the owner alone edits or deletes it;
anyone else is told it does not exist.
"""

from __future__ import annotations

import asyncio
import logging
import uuid
from datetime import datetime
from typing import Any

from sqlalchemy import delete, select, update

from app.config import settings
from app.database import AsyncSessionLocal
from app.models.workflows import (
    RUN_CANCELLED,
    RUN_COMPLETED,
    RUN_FAILED,
    RUN_RUNNING,
    STEP_COMPLETED,
    STEP_FAILED,
    STEP_PENDING,
    STEP_RUNNING,
    STEP_SKIPPED,
    Workflow,
    WorkflowRun,
    WorkflowStepRun,
)
from app.services.job_queue import job_manager
from app.services.llm_clients import make_openai
from app.services.workflows.steps import ADAPTERS, StepContext, StepError

logger = logging.getLogger(__name__)


class WorkflowError(Exception):
    """The run stopped at a step. Not a RuntimeError, so the job queue never
    retries a whole workflow because one step failed."""


class WorkflowService:
    # ------------------------------------------------------------------ access

    @staticmethod
    def can_read(workflow: Workflow, user_id: str, accessible_matter_ids: set[str] | None) -> bool:
        if workflow.owner_id == user_id:
            return True
        return bool(
            accessible_matter_ids
            and workflow.matter_id
            and workflow.matter_id in accessible_matter_ids
        )

    @staticmethod
    def role_for(workflow: Workflow, user_id: str) -> str:
        return "owner" if workflow.owner_id == user_id else "member"

    @staticmethod
    def _llm_client(user_keys: Any) -> Any:
        """The runner's OpenAI-compatible client (BYOK key, else the instance key)."""
        key = user_keys.openai if user_keys and getattr(user_keys, "openai", None) else None
        client = make_openai(key or settings.openai_api_key, async_=True)
        if client is None:
            raise ValueError(
                "An OpenAI API key or a custom LLM endpoint is required. Configure one in Settings."
            )
        return client

    @staticmethod
    def caps() -> dict[str, int]:
        return {
            "max_steps": max(1, int(settings.workflow_max_steps)),
            "max_documents_per_step": max(1, int(settings.workflow_max_documents_per_step)),
            "max_citations_per_step": max(1, int(settings.workflow_max_citations_per_step)),
        }

    # --------------------------------------------------------------- workflows

    async def create(
        self,
        session: Any,
        *,
        owner_id: str,
        matter_id: str | None,
        name: str,
        description: str | None,
        steps: list[dict[str, Any]],
        template_key: str | None = None,
    ) -> Workflow:
        workflow = Workflow(
            id=str(uuid.uuid4()),
            owner_id=owner_id,
            matter_id=matter_id,
            name=name.strip(),
            description=(description or "").strip() or None,
            template_key=template_key,
            steps=steps,
        )
        session.add(workflow)
        await session.commit()
        await session.refresh(workflow)
        return workflow

    async def list_for_user(
        self, session: Any, user_id: str, accessible_matter_ids: set[str] | None
    ) -> list[Workflow]:
        clause = Workflow.owner_id == user_id
        if accessible_matter_ids:
            clause = clause | Workflow.matter_id.in_(sorted(accessible_matter_ids))
        rows = await session.execute(
            select(Workflow).where(clause).order_by(Workflow.updated_at.desc())
        )
        return list(rows.scalars().all())

    async def get_accessible(
        self,
        session: Any,
        workflow_id: str,
        user_id: str,
        accessible_matter_ids: set[str] | None,
    ) -> Workflow | None:
        workflow: Workflow | None = await session.get(Workflow, workflow_id)
        if workflow is None or not self.can_read(workflow, user_id, accessible_matter_ids):
            return None
        return workflow

    async def update(
        self,
        session: Any,
        workflow: Workflow,
        *,
        name: str | None = None,
        description: str | None = None,
        steps: list[dict[str, Any]] | None = None,
    ) -> Workflow:
        if name is not None:
            workflow.name = name.strip()
        if description is not None:
            workflow.description = description.strip() or None
        if steps is not None:
            workflow.steps = steps
        workflow.updated_at = datetime.utcnow()
        await session.commit()
        await session.refresh(workflow)
        return workflow

    async def delete(self, session: Any, workflow: Workflow) -> None:
        # Explicit so SQLite without foreign-key enforcement also drops the runs.
        run_ids = select(WorkflowRun.id).where(WorkflowRun.workflow_id == workflow.id)
        await session.execute(delete(WorkflowStepRun).where(WorkflowStepRun.run_id.in_(run_ids)))
        await session.execute(delete(WorkflowRun).where(WorkflowRun.workflow_id == workflow.id))
        await session.delete(workflow)
        await session.commit()

    async def run_stats(self, session: Any, workflow_ids: list[str]) -> dict[str, dict[str, Any]]:
        """Per workflow: how many runs, and when and how the latest one ended."""
        if not workflow_ids:
            return {}
        rows = await session.execute(
            select(WorkflowRun.workflow_id, WorkflowRun.status, WorkflowRun.created_at)
            .where(WorkflowRun.workflow_id.in_(workflow_ids))
            .order_by(WorkflowRun.created_at.desc())
        )
        stats: dict[str, dict[str, Any]] = {}
        for workflow_id, status, created_at in rows.all():
            entry = stats.setdefault(
                str(workflow_id),
                {"run_count": 0, "last_run_at": created_at, "last_run_status": status},
            )
            entry["run_count"] += 1
        return stats

    # -------------------------------------------------------------------- runs

    async def create_run(
        self,
        session: Any,
        workflow: Workflow,
        *,
        runner_id: str,
        document_ids: list[str],
    ) -> WorkflowRun:
        """A run with the workflow's steps frozen and one pending step run per step."""
        steps = [dict(s) for s in (workflow.steps or [])]
        run = WorkflowRun(
            id=str(uuid.uuid4()),
            workflow_id=workflow.id,
            runner_id=runner_id,
            matter_id=workflow.matter_id,
            steps=steps,
            input_document_ids=list(dict.fromkeys(document_ids)),
            status=RUN_RUNNING,
        )
        session.add(run)
        for index, step in enumerate(steps):
            session.add(
                WorkflowStepRun(
                    run_id=run.id,
                    index=index,
                    name=str(step["name"]),
                    kind=str(step["kind"]),
                    status=STEP_PENDING,
                    input_document_ids=[],
                    produced_ids={},
                )
            )
        workflow.last_run_at = datetime.utcnow()
        await session.commit()
        await session.refresh(run)
        return run

    async def set_job(self, session: Any, run_id: str, job_id: str) -> None:
        await session.execute(
            update(WorkflowRun).where(WorkflowRun.id == run_id).values(job_id=job_id)
        )
        await session.commit()

    async def list_runs(self, session: Any, workflow_id: str, limit: int = 50) -> list[WorkflowRun]:
        rows = await session.execute(
            select(WorkflowRun)
            .where(WorkflowRun.workflow_id == workflow_id)
            .order_by(WorkflowRun.created_at.desc())
            .limit(max(1, min(limit, 200)))
        )
        return list(rows.scalars().all())

    async def get_run(self, session: Any, workflow_id: str, run_id: str) -> WorkflowRun | None:
        run: WorkflowRun | None = await session.get(WorkflowRun, run_id)
        if run is None or run.workflow_id != workflow_id:
            return None
        return run

    async def step_runs(self, session: Any, run_id: str) -> list[WorkflowStepRun]:
        rows = await session.execute(
            select(WorkflowStepRun)
            .where(WorkflowStepRun.run_id == run_id)
            .order_by(WorkflowStepRun.index)
        )
        return list(rows.scalars().all())

    async def completed_counts(self, session: Any, run_ids: list[str]) -> dict[str, int]:
        if not run_ids:
            return {}
        rows = await session.execute(
            select(WorkflowStepRun.run_id, WorkflowStepRun.status).where(
                WorkflowStepRun.run_id.in_(run_ids)
            )
        )
        counts: dict[str, int] = {}
        for run_id, status in rows.all():
            counts.setdefault(str(run_id), 0)
            if status == STEP_COMPLETED:
                counts[str(run_id)] += 1
        return counts

    async def prepare_resume(self, session: Any, run: WorkflowRun) -> int:
        """Reset the failed step and everything after it to pending; the index to resume at.

        Raises ValueError when the run has nothing to resume (still running,
        or completed).
        """
        if run.status not in (RUN_FAILED, RUN_CANCELLED):
            raise ValueError("Only a failed or cancelled run can be resumed")
        rows = await self.step_runs(session, run.id)
        start = next(
            (
                r.index
                for r in rows
                if r.status in (STEP_FAILED, STEP_SKIPPED, STEP_PENDING, STEP_RUNNING)
            ),
            None,
        )
        if start is None:
            raise ValueError("Every step of this run has completed")
        await session.execute(
            update(WorkflowStepRun)
            .where(WorkflowStepRun.run_id == run.id, WorkflowStepRun.index >= start)
            .values(status=STEP_PENDING, output=None, error=None, started_at=None, finished_at=None)
        )
        run.status = RUN_RUNNING
        run.error = None
        run.completed_at = None
        run.job_id = None
        run.resumed_count = int(run.resumed_count or 0) + 1
        await session.commit()
        return start

    # --------------------------------------------------------------- execution

    async def _mark_step(self, run_id: str, index: int, status: str, **values: Any) -> None:
        now = datetime.utcnow()
        if status == STEP_RUNNING:
            values["started_at"] = now
        elif status in (STEP_COMPLETED, STEP_FAILED):
            values["finished_at"] = now
        async with AsyncSessionLocal() as session:
            await session.execute(
                update(WorkflowStepRun)
                .where(WorkflowStepRun.run_id == run_id, WorkflowStepRun.index == index)
                .values(status=status, **values)
            )
            await session.commit()

    async def _skip_rest(self, run_id: str, from_index: int) -> None:
        async with AsyncSessionLocal() as session:
            await session.execute(
                update(WorkflowStepRun)
                .where(
                    WorkflowStepRun.run_id == run_id,
                    WorkflowStepRun.index >= from_index,
                    WorkflowStepRun.status == STEP_PENDING,
                )
                .values(status=STEP_SKIPPED)
            )
            await session.commit()

    async def _finish(self, run_id: str, status: str, error: str | None = None) -> None:
        async with AsyncSessionLocal() as session:
            await session.execute(
                update(WorkflowRun)
                .where(WorkflowRun.id == run_id)
                .values(status=status, error=error, completed_at=datetime.utcnow())
            )
            await session.commit()

    async def _fail(self, run_id: str, index: int, total: int, name: str, message: str) -> None:
        await self._mark_step(run_id, index, STEP_FAILED, error=message[:1000])
        await self._skip_rest(run_id, index + 1)
        await self._finish(
            run_id, RUN_FAILED, f"Step {index + 1}/{total} '{name}' failed: {message}"
        )

    async def execute(
        self,
        *,
        run_id: str,
        user_id: str,
        accessible_matter_ids: set[str] | None,
        user_keys: Any,
        start_index: int = 0,
    ) -> dict[str, Any]:
        """Run the steps from ``start_index``; the job body. Persists every outcome."""
        async with AsyncSessionLocal() as session:
            run = await session.get(WorkflowRun, run_id)
            if run is None:
                raise LookupError("Workflow run not found")
            workflow = await session.get(Workflow, run.workflow_id)
            steps = [dict(s) for s in (run.steps or [])]
            input_ids = list(run.input_document_ids or [])
            matter_id = run.matter_id
            workflow_name = workflow.name if workflow else "Workflow"
            outputs = {
                r.name: r.output
                for r in await self.step_runs(session, run_id)
                if r.status == STEP_COMPLETED and r.output
            }

        total = len(steps)
        try:
            client = self._llm_client(user_keys)
        except ValueError as e:
            await self._finish(run_id, RUN_FAILED, str(e))
            raise WorkflowError(str(e)) from e
        caps = self.caps()
        ctx = StepContext(
            run_id=run_id,
            workflow_name=workflow_name,
            user_id=user_id,
            accessible_matter_ids=accessible_matter_ids,
            user_keys=user_keys,
            client=client,
            matter_id=matter_id,
            input_document_ids=input_ids,
            outputs=outputs,
            max_documents=caps["max_documents_per_step"],
            max_citations=caps["max_citations_per_step"],
        )

        produced: dict[str, list[str]] = {}
        for index in range(start_index, total):
            step = steps[index]
            name = str(step["name"])
            label = f"Step {index + 1}/{total} ({name})"
            fraction = index / total if total else 0.0

            def report(message: str, _label: str = label, _fraction: float = fraction) -> None:
                job_manager.report_progress(f"{_label}: {message}", _fraction)

            ctx.progress = report
            await self._mark_step(run_id, index, STEP_RUNNING)
            job_manager.report_progress(f"{label}: starting", fraction)
            adapter = ADAPTERS.get(str(step["kind"]))
            if adapter is None:
                await self._fail(run_id, index, total, name, f"Unknown step kind {step['kind']}")
                raise WorkflowError(f"Unknown step kind {step['kind']}")
            try:
                output = await adapter(step, ctx)
            except StepError as e:
                await self._fail(run_id, index, total, name, str(e))
                raise WorkflowError(f"Step '{name}' failed: {e}") from e
            except asyncio.CancelledError:
                await self._mark_step(run_id, index, STEP_FAILED, error="Cancelled")
                await self._skip_rest(run_id, index + 1)
                await self._finish(run_id, RUN_CANCELLED, "Cancelled before completion")
                raise
            except Exception as e:  # a library's own exception class: still one failed step
                logger.error("Workflow run %s step %s failed: %s", run_id, name, e, exc_info=True)
                await self._fail(run_id, index, total, name, f"{type(e).__name__}: {e}")
                raise WorkflowError(f"Step '{name}' failed unexpectedly") from e

            as_json = output.as_json()
            await self._mark_step(
                run_id,
                index,
                STEP_COMPLETED,
                output=as_json,
                input_document_ids=list(output.document_ids),
                produced_ids={k: list(v) for k, v in output.produced.items()},
            )
            ctx.outputs[name] = as_json
            for key, ids in output.produced.items():
                produced.setdefault(key, []).extend(ids)

        await self._finish(run_id, RUN_COMPLETED)
        job_manager.report_progress("Workflow complete", 1.0)
        return {
            "run_id": run_id,
            "status": RUN_COMPLETED,
            "steps": total,
            "completed_steps": total,
            "documents": len(input_ids),
            "produced": {k: len(v) for k, v in produced.items()},
        }


workflow_service = WorkflowService()
