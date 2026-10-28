"""Unit tests for the workflow building blocks: step validation (the 422
cases), ``{{step.field}}`` substitution, input resolution across steps, the
citation finder, and the step adapters against stubbed services.
"""

import os

os.environ.setdefault("SECRET_KEY", "test-secret-key-for-testing-only-32chars!")
os.environ.setdefault("ENCRYPTION_SALT", "test-salt-16chars!")
os.environ.setdefault("DEBUG", "true")

import asyncio
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from app.models.schemas import WorkflowCreateRequest
from app.services.review_tables import review_table_service
from app.services.workflows import steps as steps_module
from app.services.workflows.steps import (
    StepContext,
    StepError,
    find_citations,
    resolve_inputs,
    run_citation_check,
    run_compare_contracts,
    run_deep_research,
    run_draft_document,
)
from app.services.workflows.substitution import output_as_text, render_template
from app.services.workflows.templates import TEMPLATES, template_steps
from pydantic import ValidationError

from tests.helpers import make_user, register_document

FIELDS = [{"name": "Governing law", "type": "text"}, {"name": "Term", "type": "duration"}]


def _step(name: str, kind: str, params: dict | None = None, inputs: dict | None = None) -> dict:
    return {"name": name, "kind": kind, "params": params or {}, "inputs": inputs or {}}


def _request(*steps: dict) -> WorkflowCreateRequest:
    return WorkflowCreateRequest(name="wf", description=None, matter_id=None, steps=list(steps))


# ------------------------------------------------------------------ validation


def test_steps_normalise_params_and_derive_field_keys():
    req = _request(
        _step("extract", "extract_fields", {"fields": FIELDS}),
        _step("review", "analyze_contract", {}),
    )
    extract, review = req.steps
    assert [f["key"] for f in extract.params["fields"]] == ["governing_law", "term"]
    assert extract.inputs.source.value == "run" and extract.inputs.document_ids == []
    # Omitted params come back as the validated defaults, never as an empty dict.
    assert review.params == {
        "contract_type": "auto",
        "jurisdiction": None,
        "representing": None,
        "posture": "balanced",
        "instructions": None,
    }


@pytest.mark.parametrize(
    "steps, message",
    [
        ([_step("a", "make_coffee")], "kind"),
        ([_step("a", "extract_fields", {"fields": []})], "fields"),
        (
            [_step("a", "extract_fields", {"fields": [{"name": "Term"}, {"name": "term"}]})],
            "Duplicate",
        ),
        ([_step("a", "analyze_contract", {"posture": "aggressive"})], "posture"),
        ([_step("a", "draft_document", {"instructions": "x"})], "instructions"),
        ([_step("bad name!", "analyze_contract")], "Step names"),
        ([_step("a", "analyze_contract"), _step("A", "analyze_contract")], "Duplicate step name"),
        (
            [
                _step("a", "citation_check", {"from_steps": ["later"]}),
                _step("later", "analyze_contract"),
            ],
            "not an earlier step",
        ),
        (
            [_step("a", "analyze_contract", inputs={"source": "step", "step": "nope"})],
            "not an earlier step",
        ),
        (
            [_step("a", "deep_research", {"question": "What about {{a.summary}}?"})],
            "refers to itself",
        ),
        (
            [_step("a", "draft_document", {"instructions": "Use {{missing.data}} here"})],
            "not an earlier step",
        ),
        (
            [_step("a", "analyze_contract", inputs={"source": "documents"})],
            "document_ids is required",
        ),
        ([_step("a", "analyze_contract", inputs={"source": "step"})], "inputs.step is required"),
    ],
)
def test_invalid_steps_are_rejected(steps, message):
    with pytest.raises(ValidationError) as excinfo:
        _request(*steps)
    assert message in str(excinfo.value)


def test_references_to_earlier_steps_are_accepted_case_insensitively():
    req = _request(
        _step("Extract", "extract_fields", {"fields": FIELDS}),
        _step("research", "deep_research", {"question": "Given {{extract.data.values}}, what?"}),
        _step(
            "memo",
            "draft_document",
            {
                "instructions": "Write it: {{Research.data.answer}}",
                "include_outputs_from": ["extract", "research"],
            },
        ),
        _step(
            "cites",
            "citation_check",
            {"from_steps": ["memo"]},
            inputs={"source": "step", "step": "extract"},
        ),
    )
    assert [s.name for s in req.steps] == ["Extract", "research", "memo", "cites"]


def test_builtin_templates_validate_and_cover_the_promised_steps():
    kinds = {t["key"]: [s.kind.value for s in template_steps(t)] for t in TEMPLATES}
    assert kinds == {
        "nda_intake": ["extract_fields", "analyze_contract", "citation_check"],
        "lease_review": ["extract_fields", "compare_contracts", "draft_document"],
        "due_diligence": ["extract_fields", "deep_research", "draft_document"],
    }


# ---------------------------------------------------------------- substitution


def test_render_template_walks_dicts_lists_and_reports_missing_paths():
    outputs = {
        "extract": {
            "summary": "Extracted 2 fields.",
            "data": {"values": {"d1": {"term": "12 months"}}, "list": [{"x": 1}, {"x": True}]},
        }
    }
    rendered = render_template(
        "A {{extract}} B {{ Extract.data.values.d1.term }} C {{extract.data.list.1.x}} "
        "D {{extract.data.nope}} E {{other.summary}} F {{extract.data.list.9}}",
        outputs,
    )
    assert rendered.text == "A Extracted 2 fields. B 12 months C yes D  E  F "
    assert rendered.used == ["extract", "Extract.data.values.d1.term", "extract.data.list.1.x"]
    assert rendered.missing == ["extract.data.nope", "other.summary", "extract.data.list.9"]


def test_render_template_serialises_objects_and_caps_their_length():
    big = {"big": {"summary": "s", "data": {"blob": "x" * 20_000, "obj": {"a": [1, 2]}}}}
    rendered = render_template("{{big.data.blob}}|{{big.data.obj}}", big)
    blob, obj = rendered.text.split("|")
    assert len(blob) == 12_000 and obj == '{"a": [1, 2]}'
    assert output_as_text({"summary": "Sum", "data": {"k": "v"}}, 100).startswith("Sum\n{")
    assert output_as_text({"summary": "Sum", "data": None}, 100) == "Sum"


# -------------------------------------------------------------- input resolution


def _ctx(user, **overrides) -> StepContext:
    base = dict(
        run_id="run-1",
        workflow_name="WF",
        user_id=user.id,
        accessible_matter_ids=set(),
        user_keys=None,
        client=MagicMock(),
        matter_id=None,
        input_document_ids=["d1", "d2"],
        outputs={},
    )
    base.update(overrides)
    return StepContext(**base)


def test_resolve_inputs_follows_run_documents_and_earlier_steps():
    user = make_user("u")
    ctx = _ctx(user)
    assert resolve_inputs(_step("a", "analyze_contract"), ctx) == ["d1", "d2"]
    explicit = _step(
        "a", "analyze_contract", inputs={"source": "documents", "document_ids": ["x", "y", "x"]}
    )
    assert resolve_inputs(explicit, ctx) == ["x", "y"]

    ctx.outputs["Extract"] = {"summary": "s", "data": {}, "document_ids": ["d2"], "produced": {}}
    from_step = _step("b", "analyze_contract", inputs={"source": "step", "step": "extract"})
    assert resolve_inputs(from_step, ctx) == ["d2"]
    ctx.outputs["Extract"]["produced"] = {"documents": ["new-1"]}
    assert resolve_inputs(from_step, ctx) == ["new-1"]

    with pytest.raises(StepError, match="has not completed"):
        resolve_inputs(
            _step("c", "analyze_contract", inputs={"source": "step", "step": "ghost"}), ctx
        )


# --------------------------------------------------------------- citation check


def test_find_citations_normalises_and_deduplicates():
    text = 'See Acme v. Beta, 123 F.3d  456 (2d Cir. 2020); also 123 F.3d 456 and 2021 WL 12345, "410 U.S. 113".'
    assert find_citations(text) == ["123 F.3d 456", "410 U.S. 113", "2021 WL 12345"]


def test_citation_check_scans_prior_outputs_and_caps_the_count():
    user = make_user("u")
    ctx = _ctx(
        user,
        max_citations=2,
        outputs={
            "review": {"summary": "Cites 123 F.3d 456 and 410 U.S. 113.", "data": {}},
            "other": {"summary": "Cites 2021 WL 12345.", "data": {}},
        },
    )
    seen: list[str] = []

    async def fake_check(citation: str) -> dict:
        seen.append(citation)
        negative = 1 if citation.startswith("123") else 0
        return {"citation": citation, "status": "checked", "negative": negative, "caution": 0}

    with patch.object(steps_module, "check_citation", AsyncMock(side_effect=fake_check)):
        out = asyncio.run(
            run_citation_check(
                _step("c", "citation_check", {"citations": ["1 F.3d 1"], "from_steps": None}), ctx
            )
        )
    # Explicit citations first, then those found in every prior output, capped at two.
    assert seen == ["1 F.3d 1", "123 F.3d 456"]
    assert out.data["skipped"] == ["410 U.S. 113", "2021 WL 12345"]
    assert out.data["found_in_steps"] == {
        "review": ["123 F.3d 456", "410 U.S. 113"],
        "other": ["2021 WL 12345"],
    }
    assert out.summary.startswith("Checked 2 citations: 1 with negative treatment language")

    empty = asyncio.run(
        run_citation_check(_step("c", "citation_check", {"citations": [], "from_steps": []}), ctx)
    )
    assert empty.summary == "No citations to check." and empty.data["citations"] == []

    with pytest.raises(StepError, match="has no output"):
        asyncio.run(
            run_citation_check(
                _step("c", "citation_check", {"citations": [], "from_steps": ["ghost"]}), ctx
            )
        )


# --------------------------------------------------------------------- compare


def test_compare_uses_the_first_input_as_baseline_and_counts_differences(client):
    user = make_user("u")
    a = register_document(user, filename="a.pdf")
    b = register_document(user, filename="b.pdf")
    c = register_document(user, filename="c.pdf")
    ctx = _ctx(user, input_document_ids=[a, b, c])
    compared: list[tuple[str, str]] = []

    async def fake_compare(client_, text_a, text_b, label_a, label_b, focus=None):
        compared.append((label_a, label_b))
        return {
            "overall": "Mostly the same.",
            "clauses": [
                {"topic": "Term", "status": "changed"},
                {"topic": "Notice", "status": "unchanged"},
            ],
            "label_a": label_a,
            "label_b": label_b,
        }

    with (
        patch.object(review_table_service, "document_text", AsyncMock(return_value="TEXT")),
        patch.object(steps_module, "compare_contracts", AsyncMock(side_effect=fake_compare)),
    ):
        out = asyncio.run(
            run_compare_contracts(_step("cmp", "compare_contracts", {"focus": "term"}), ctx)
        )
    assert compared == [("a.pdf", "b.pdf"), ("a.pdf", "c.pdf")]
    assert out.data["baseline_document_id"] == a
    assert [c["differences"] for c in out.data["comparisons"]] == [1, 1]
    assert out.summary == "Compared 2 documents against a.pdf: 2 clause differences flagged."
    assert out.document_ids == [a, b, c]

    with pytest.raises(StepError, match="at least one other"):
        asyncio.run(
            run_compare_contracts(
                _step("cmp", "compare_contracts", {}), _ctx(user, input_document_ids=[a])
            )
        )


# ----------------------------------------------------------------------- draft


def test_draft_renders_the_template_and_attaches_prior_outputs(client):
    user = make_user("u")
    ref = register_document(user, filename="ref.pdf")
    ctx = _ctx(
        user,
        outputs={
            "extract": {
                "summary": "Extracted 1 field.",
                "data": {"values": {"d1": {"term": "1 year"}}},
            }
        },
    )
    captured: dict = {}

    async def fake_draft(client_, instructions, reference_text=None, revision_of=None):
        captured["instructions"] = instructions
        captured["reference"] = reference_text
        return {"title": "Memo", "text": "one two three", "case_law_removed": []}

    settings = MagicMock(
        contract_playbook="PLAYBOOK", practice_profile="PROFILE", practice_area="M&A"
    )
    with (
        patch.object(
            review_table_service, "document_text", AsyncMock(return_value="REFERENCE TEXT")
        ),
        patch.object(steps_module, "draft_document", AsyncMock(side_effect=fake_draft)),
        patch.object(steps_module, "load_user_settings", return_value=settings),
    ):
        out = asyncio.run(
            run_draft_document(
                _step(
                    "memo",
                    "draft_document",
                    {
                        "instructions": "Summarise {{extract.data.values.d1.term}} and {{extract.data.nope}}",
                        "include_outputs_from": ["extract"],
                        "reference_document_ids": [ref],
                    },
                ),
                ctx,
            )
        )
    assert captured["instructions"].startswith(
        "THE USER'S PRACTICE PROFILE (M&A):\nPROFILE\n\nPLAYBOOK\n\n"
    )
    assert captured["instructions"].endswith("Summarise 1 year and")
    assert "[Output of step extract]\nExtracted 1 field." in captured["reference"]
    assert "[ref.pdf]\nREFERENCE TEXT" in captured["reference"]
    assert out.summary == 'Drafted "Memo" (3 words).'
    assert out.data["template_values_missing"] == ["extract.data.nope"]
    assert out.data["references"] == [
        {"kind": "step", "name": "extract"},
        {"kind": "document", "id": ref, "filename": "ref.pdf"},
    ]


# --------------------------------------------------------------- deep research


def test_deep_research_renders_the_question_narrows_tools_and_scopes_documents(client):
    user = make_user("u")
    d1 = register_document(user, filename="a.pdf")
    ctx = _ctx(
        user,
        input_document_ids=[d1],
        outputs={"extract": {"summary": "Extracted.", "data": {"values": {"x": 1}}}},
    )
    service = steps_module.deep_research_service
    plan = [
        {"id": "q1", "question": "Docs?", "tools": ["documents", "case_law"]},
        {"id": "q2", "question": "Law?", "tools": ["case_law"]},
    ]
    run_result = {
        "answer": "It depends [P1].",
        "key_findings": [],
        "open_questions": [],
        "sources": [
            {"ref": "P1", "title": "a.pdf", "citation": None, "kind": "document", "url": None}
        ],
        "verification": {"quotes_verified": 1},
    }
    with (
        patch.object(service, "plan", AsyncMock(return_value=plan)) as plan_mock,
        patch.object(service, "run", AsyncMock(return_value=run_result)) as run_mock,
    ):
        out = asyncio.run(
            run_deep_research(
                _step(
                    "research",
                    "deep_research",
                    {"question": "Given {{extract.data.values}}, what?", "tools": ["documents"]},
                ),
                ctx,
            )
        )
    assert plan_mock.await_args.args[1] == 'Given {"x": 1}, what?'
    kwargs = run_mock.await_args.kwargs
    assert kwargs["document_ids"] == [d1] and kwargs["user_id"] == user.id
    assert out.data["answer"] == "It depends [P1]." and out.produced["research_runs"] == [
        kwargs["run_id"]
    ]
    assert out.summary == "Researched 2 sub-questions; 1 source cited, 1 quotes verified."

    # The persisted run carries the narrowed plan: case_law was not allowed.
    from app.models.deep_research import DeepResearchRun
    from sqlalchemy import select
    from sqlalchemy.orm import Session

    from tests.helpers import _sync_engine

    engine = _sync_engine()
    try:
        with Session(engine) as session:
            row = session.execute(
                select(DeepResearchRun).where(DeepResearchRun.id == kwargs["run_id"])
            ).scalar_one()
    finally:
        engine.dispose()
    assert [s["tools"] for s in row.plan] == [["documents"], ["documents"]]
    assert row.scope["workflow_run_id"] == "run-1" and row.scope["document_ids"] == [d1]
