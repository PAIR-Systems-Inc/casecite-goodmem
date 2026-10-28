"""Built-in starter workflows. Each is a plain step list a user instantiates
into a workflow of their own, then edits freely; the template itself never
changes. They exist so the feature demonstrates itself: three common,
linear review processes expressed as the steps the platform already has.
"""

from __future__ import annotations

from typing import Any

from app.models.schemas import WorkflowCreateRequest, WorkflowStep

_NDA_FIELDS = [
    {"name": "Counterparty", "description": "The other party to the agreement", "type": "text"},
    {"name": "Effective date", "description": "When the agreement takes effect", "type": "date"},
    {
        "name": "Term",
        "description": "How long confidentiality obligations last",
        "type": "duration",
    },
    {"name": "Mutual", "description": "Do both parties disclose and receive?", "type": "boolean"},
    {
        "name": "Governing law",
        "description": "State or country whose law governs",
        "type": "text",
    },
    {
        "name": "Residuals clause",
        "description": "May the recipient use information retained in unaided memory?",
        "type": "boolean",
    },
]

_LEASE_FIELDS = [
    {"name": "Landlord", "type": "text"},
    {"name": "Tenant", "type": "text"},
    {"name": "Premises", "description": "Address or description of the space", "type": "text"},
    {"name": "Commencement date", "type": "date"},
    {"name": "Term", "type": "duration"},
    {"name": "Base rent", "description": "Monthly or annual base rent", "type": "money"},
    {
        "name": "Renewal option",
        "description": "Does the tenant have an option to renew?",
        "type": "boolean",
    },
    {
        "name": "Assignment",
        "description": "May the tenant assign or sublet without consent?",
        "type": "boolean",
    },
]

_DILIGENCE_FIELDS = [
    {"name": "Counterparty", "type": "text"},
    {"name": "Contract type", "description": "e.g. MSA, SaaS, supply, employment", "type": "text"},
    {"name": "Term", "type": "duration"},
    {
        "name": "Change of control",
        "description": "Does a change of control need consent or trigger termination?",
        "type": "boolean",
    },
    {"name": "Exclusivity", "description": "Any exclusivity or non-compete?", "type": "boolean"},
    {"name": "Liability cap", "type": "money"},
    {"name": "Governing law", "type": "text"},
]

TEMPLATES: list[dict[str, Any]] = [
    {
        "key": "nda_intake",
        "name": "NDA intake",
        "description": (
            "Extract the key terms of every incoming NDA, run the full contract review on "
            "each, and check any authorities the review cites."
        ),
        "steps": [
            {
                "name": "extract",
                "kind": "extract_fields",
                "params": {"fields": _NDA_FIELDS},
                "inputs": {"source": "run"},
            },
            {
                "name": "review",
                "kind": "analyze_contract",
                "params": {"contract_type": "nda", "posture": "balanced"},
                "inputs": {"source": "run"},
            },
            {
                "name": "citations",
                "kind": "citation_check",
                "params": {"from_steps": ["review"]},
                "inputs": {"source": "run"},
            },
        ],
    },
    {
        "key": "lease_review",
        "name": "Lease review",
        "description": (
            "Extract the commercial terms of each lease, compare every lease clause by "
            "clause against the first one as the baseline, and draft a summary of the "
            "differences."
        ),
        "steps": [
            {
                "name": "extract",
                "kind": "extract_fields",
                "params": {"fields": _LEASE_FIELDS},
                "inputs": {"source": "run"},
            },
            {
                "name": "compare",
                "kind": "compare_contracts",
                "params": {"focus": "rent, term, renewal, assignment and repair obligations"},
                "inputs": {"source": "run"},
            },
            {
                "name": "summary",
                "kind": "draft_document",
                "params": {
                    "instructions": (
                        "Write a lease comparison memo for the supervising attorney. Open "
                        "with a table of the extracted terms, then summarise the material "
                        "differences the comparison found, clause by clause, and close with "
                        "the points that need negotiation. Extracted terms: {{extract.data.values}}"
                    ),
                    "include_outputs_from": ["compare"],
                },
                "inputs": {"source": "run"},
            },
        ],
    },
    {
        "key": "due_diligence",
        "name": "Due-diligence pass",
        "description": (
            "Extract the deal-relevant terms from a data-room batch, research the "
            "change-of-control and assignment exposure across the batch and the case law, "
            "and draft a findings memo."
        ),
        "steps": [
            {
                "name": "extract",
                "kind": "extract_fields",
                "params": {"fields": _DILIGENCE_FIELDS},
                "inputs": {"source": "run"},
            },
            {
                "name": "research",
                "kind": "deep_research",
                "params": {
                    "question": (
                        "Across these contracts, which agreements need counterparty consent "
                        "on a change of control or assignment, and how have courts treated "
                        "anti-assignment and change-of-control clauses in a sale of the "
                        "business? Extracted terms: {{extract.data.values}}"
                    )
                },
                "inputs": {"source": "run"},
            },
            {
                "name": "memo",
                "kind": "draft_document",
                "params": {
                    "instructions": (
                        "Draft a due-diligence findings memo. Summarise the extracted terms "
                        "by contract, set out the change-of-control and assignment exposure "
                        "the research found with its sources, and list the consents and "
                        "notices the transaction will need. Research answer: "
                        "{{research.data.answer}}"
                    ),
                    "include_outputs_from": ["extract", "research"],
                },
                "inputs": {"source": "run"},
            },
        ],
    },
]


def template_by_key(key: str) -> dict[str, Any] | None:
    return next((t for t in TEMPLATES if t["key"] == key), None)


def template_steps(template: dict[str, Any]) -> list[WorkflowStep]:
    """The template's steps validated exactly as a user's would be."""
    request = WorkflowCreateRequest(
        name=template["name"], description=None, matter_id=None, steps=template["steps"]
    )
    return request.steps
