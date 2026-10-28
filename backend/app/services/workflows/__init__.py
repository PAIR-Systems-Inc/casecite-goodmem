"""Workflows: saved, repeatable, linear sequences of the platform's own
operations (extract fields, analyse, compare, draft, research, check
citations), run as one background job with every step's outcome persisted.

See ``docs/workflows.md`` for the step kinds, their parameters and the
built-in templates.
"""

from app.services.workflows.service import WorkflowError, WorkflowService, workflow_service
from app.services.workflows.steps import StepError

__all__ = ["StepError", "WorkflowError", "WorkflowService", "workflow_service"]
