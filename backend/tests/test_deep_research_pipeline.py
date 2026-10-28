"""Deep research pipeline units: plan normalisation, reference joining, the
passage store and its cap, opinion excerpts, and the runner end to end with
faked tools and a scripted model — verified vs unverified quotes, stripped
invalid refs, the concurrency cap, the tool budget and the follow-up depth.
"""

import asyncio
import json
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from app.services.deep_research import pipeline
from app.services.deep_research.passages import (
    PassageStore,
    extract_refs,
    join_refs_in_text,
    verify_claim_quote,
)
from app.services.deep_research.pipeline import ResearchRunner, RunConfig, SynthesisError
from app.services.deep_research.planner import PlanningError, normalise_plan, plan_research
from app.services.deep_research.tools import (
    ToolBudget,
    ToolBudgetExhausted,
    best_excerpt,
    strip_html,
)

AVAILABLE = {"documents": True, "case_law": True}

DOC_TEXT = (
    "LIMITATION OF LIABILITY. Neither party shall be liable for consequential damages. "
    "The aggregate liability of Supplier is capped at the fees paid in the preceding twelve months."
)
CASE_TEXT = (
    "A limitation of liability clause is enforceable between sophisticated commercial parties "
    "absent fraud or wilful misconduct. We therefore affirm."
)


def _response(payload: dict) -> MagicMock:
    response = MagicMock()
    response.choices = [MagicMock()]
    response.choices[0].message.content = json.dumps(payload)
    return response


# ------------------------------------------------------------------- planner


class TestPlanner:
    def test_normalise_assigns_ids_drops_unknown_tools_and_caps(self):
        raw = {
            "sub_questions": [
                {
                    "question": "What does the contract say about liability?",
                    "tools": ["documents", "web"],
                },
                {
                    "question": "what does the contract say about liability?",
                    "tools": ["documents"],
                },  # dupe
                {"question": "Are such caps enforceable?", "tools": ["CASE_LAW"]},
                {"question": "x", "tools": ["documents"]},  # too short
                {"question": "Anything else?", "tools": []},  # no tools -> all available
                {"question": "One too many", "tools": ["documents"]},
            ]
        }
        plan = normalise_plan(raw, AVAILABLE, max_subquestions=3)
        assert [s["id"] for s in plan] == ["q1", "q2", "q3"]
        assert plan[0]["tools"] == ["documents"]
        assert plan[1]["tools"] == ["case_law"]
        assert plan[2]["tools"] == ["documents", "case_law"]

    def test_normalise_respects_tool_availability(self):
        raw = {"sub_questions": [{"question": "Any case law?", "tools": ["case_law"]}]}
        plan = normalise_plan(raw, {"documents": True, "case_law": False}, 8)
        assert plan[0]["tools"] == ["documents"]  # falls back to what is available

    @pytest.mark.asyncio
    async def test_plan_research_returns_plan_and_raises_when_unusable(self):
        good = _response(
            {"sub_questions": [{"question": "What is the cap?", "tools": ["documents"]}]}
        )
        with (
            patch.object(pipeline, "openai_chat"),
            patch("app.services.deep_research.planner.openai_chat", AsyncMock(return_value=good)),
        ):
            plan = await plan_research(
                MagicMock(),
                "Is the liability cap enforceable?",
                available=AVAILABLE,
                max_subquestions=8,
            )
        assert plan == [{"id": "q1", "question": "What is the cap?", "tools": ["documents"]}]

        with (
            patch(
                "app.services.deep_research.planner.openai_chat",
                AsyncMock(return_value=_response({})),
            ),
            pytest.raises(PlanningError),
        ):
            await plan_research(MagicMock(), "question?", available=AVAILABLE, max_subquestions=8)
        with (
            patch(
                "app.services.deep_research.planner.openai_chat",
                AsyncMock(side_effect=RuntimeError("down")),
            ),
            pytest.raises(PlanningError),
        ):
            await plan_research(MagicMock(), "question?", available=AVAILABLE, max_subquestions=8)


# ------------------------------------------------------------------ passages


class TestPassages:
    def test_store_assigns_sequential_refs_dedupes_and_caps(self):
        store = PassageStore(max_passages=2)
        a = store.add("doc:1:0", "q1", source_type="document", text="a", title="a.pdf")
        again = store.add("doc:1:0", "q2", source_type="document", text="a", title="a.pdf")
        b = store.add("doc:1:1", "q1", source_type="document", text="b", title="a.pdf")
        c = store.add("doc:1:2", "q1", source_type="document", text="c", title="a.pdf")
        assert a is again and a.ref == "P1" and a.sub_question_ids == ["q1", "q2"]
        assert b is not None and b.ref == "P2"
        assert c is None and store.dropped_over_cap == 1 and store.full
        assert [p.ref for p in store.for_sub_question("q2")] == ["P1"]

    def test_extract_refs_accepts_only_server_refs(self):
        assert extract_refs(["P1", "[P2]", "p3", "doc-4", 5, "P1"]) == ["P1", "P2"]
        assert extract_refs("P1") == []

    def test_join_refs_in_text_strips_unknown_and_keeps_ordinary_brackets(self):
        text = "Caps are enforceable [P1, P9]. Fraud is excluded [P9]. See [section 4] and [P2]."
        clean, cited, stripped = join_refs_in_text(text, {"P1", "P2"})
        assert clean == "Caps are enforceable [P1]. Fraud is excluded. See [section 4] and [P2]."
        assert cited == ["P1", "P2"] and stripped == 2

    def test_verify_claim_quote_locates_quote_in_cited_passage_only(self):
        store = PassageStore(10)
        store.add("d", "q1", source_type="document", text=DOC_TEXT, title="msa.pdf")
        store.add("c", "q1", source_type="case_law", text=CASE_TEXT, title="Acme v. Beta")
        hit = verify_claim_quote(
            store, ["P1"], "capped at the fees paid in the preceding twelve months"
        )
        assert hit == {"verified": True, "ref": "P1", "empty": False}
        miss = verify_claim_quote(
            store, ["P1"], "absent fraud or wilful misconduct"
        )  # that is in P2
        assert miss["verified"] is False and miss["ref"] is None
        assert verify_claim_quote(store, ["P1"], "")["empty"] is True


# --------------------------------------------------------------------- tools


class TestTools:
    def test_best_excerpt_is_a_contiguous_slice_around_the_relevant_paragraph(self):
        filler = "\n\n".join(
            f"Paragraph {i} discusses procedural history and nothing more." for i in range(300)
        )
        target = "The court held that a limitation of liability clause binds sophisticated parties."
        text = filler + "\n\n" + target + "\n\n" + filler
        excerpt = best_excerpt(text, "limitation of liability clause enforceable", max_chars=1200)
        assert len(excerpt) <= 1200
        assert target in excerpt
        assert excerpt in text  # verbatim slice, so quotes verify against the opinion

    def test_best_excerpt_returns_short_text_whole_and_strips_html(self):
        assert best_excerpt("short", "q", max_chars=100) == "short"
        assert strip_html("<b>held</b> &amp; affirmed") == "held  & affirmed"
        assert strip_html("<em>x</em>") == "x"

    def test_tool_budget_refuses_past_the_cap(self):
        budget = ToolBudget(max_calls=2)
        budget.take()
        budget.take()
        assert budget.remaining == 0
        with pytest.raises(ToolBudgetExhausted):
            budget.take()


# -------------------------------------------------------------------- runner


def _doc_hit(doc_id: str, chunk: int, text: str, filename: str = "msa.pdf") -> dict:
    return {
        "id": f"{doc_id}-{chunk}",
        "text": text,
        "metadata": {"document_id": doc_id, "chunk_index": chunk, "filename": filename},
        "similarity": 0.8,
    }


def _case_hit(opinion_id: str, text: str, citation: str | None = "123 F.3d 456") -> dict:
    return {
        "case_name": "Acme v. Beta",
        "citation": citation,
        "opinion_id": opinion_id,
        "cluster_id": "9",
        "url": f"https://www.courtlistener.com/opinion/{opinion_id}/",
        "court": "ca2",
        "date_filed": "2020-01-01",
        "text": text,
        "excerpt_source": "opinion",
        "score": 12.0,
    }


def _scripted_model(answers: dict[str, dict], synthesis: dict) -> AsyncMock:
    """A fake openai_chat: answers keyed by the sub-question text, else the synthesis."""

    async def chat(client, **kwargs):
        prompt = kwargs["messages"][0]["content"]
        for question, payload in answers.items():
            if f"SUB-QUESTION: {question}" in prompt:
                return _response(payload)
        if "SUB-ANSWERS:" in prompt:
            return _response(synthesis)
        raise AssertionError("unexpected prompt")

    return AsyncMock(side_effect=chat)


def _config(**overrides) -> RunConfig:
    base = dict(
        user_id="u1",
        user_keys=None,
        document_ids=["d1"],
        matter_ids=None,
        concurrency=3,
        max_tool_calls=24,
        max_passages=60,
        followup_depth=1,
        citation_check_available=True,
    )
    base.update(overrides)
    return RunConfig(**base)


PLAN = [
    {"id": "q1", "question": "What does the contract say?", "tools": ["documents"]},
    {"id": "q2", "question": "Are such caps enforceable?", "tools": ["case_law"]},
]


@pytest.mark.asyncio
async def test_runner_verifies_quotes_strips_invalid_refs_and_cites_only_real_passages():
    answers = {
        "What does the contract say?": {
            "answer": "The cap equals twelve months of fees [P1]. Something invented [P7].",
            "claims": [
                {
                    "text": "Liability is capped at twelve months of fees.",
                    "refs": ["P1"],
                    "quote": "capped at the fees paid in the preceding twelve months",
                },
                {
                    "text": "Consequential damages are excluded.",
                    "refs": ["P1"],
                    "quote": "this sentence is not in the passage",
                },
                {"text": "A claim with a made-up source.", "refs": ["P7"], "quote": "x"},
            ],
            "followup": "",
        },
        "Are such caps enforceable?": {
            "answer": "Yes, between sophisticated parties [P2].",
            "claims": [
                {
                    "text": "Caps bind sophisticated commercial parties.",
                    "refs": ["P2"],
                    "quote": "enforceable between sophisticated commercial parties",
                },
            ],
            "followup": "",
        },
    }
    synthesis = {
        "answer": "## Answer\nThe cap is enforceable [P1, P2]. Unknown [P9].",
        "key_findings": [{"text": "The cap holds.", "refs": ["P2", "P9"]}],
        "open_questions": ["Whether fraud is alleged"],
    }
    with (
        patch.object(pipeline, "openai_chat", _scripted_model(answers, synthesis)),
        patch.object(
            pipeline, "search_firm_documents", AsyncMock(return_value=[_doc_hit("d1", 0, DOC_TEXT)])
        ),
        patch.object(
            pipeline, "search_case_law", AsyncMock(return_value=[_case_hit("77", CASE_TEXT)])
        ),
        patch.object(
            pipeline,
            "check_citation",
            AsyncMock(
                return_value={
                    "citation": "123 F.3d 456",
                    "status": "checked",
                    "warning_level": "none",
                    "is_good_law": True,
                    "case_name": "Acme v. Beta",
                    "url": None,
                    "negative": 0,
                    "caution": 0,
                }
            ),
        ) as checker,
    ):
        result = await ResearchRunner(MagicMock(), _config()).run("Is the cap enforceable?", PLAN)

    assert result["answer"] == "## Answer\nThe cap is enforceable [P1, P2]. Unknown."
    assert result["key_findings"] == [{"text": "The cap holds.", "refs": ["P2"]}]
    assert result["open_questions"] == ["Whether fraud is alleged"]

    q1, q2 = result["sub_questions"]
    assert (
        q1["status"] == "completed"
        and q1["answer"] == "The cap equals twelve months of fees [P1]. Something invented."
    )
    assert [c["verified"] for c in q1["claims"]] == [True, False]  # the P7 claim is gone
    assert q1["quotes_verified"] == 1 and q1["quotes_unverified"] == 1
    assert q1["invalid_refs_stripped"] == 2  # [P7] in the answer text + the P7 claim
    assert q2["claims"][0]["verified"] is True and q2["claims"][0]["quote_ref"] == "P2"

    refs = [s["ref"] for s in result["sources"]]
    assert refs == ["P1", "P2"]
    doc, case = result["sources"]
    assert doc["type"] == "document" and doc["document_id"] == "d1" and doc["quotes_verified"] == 1
    assert case["type"] == "case_law" and case["citation"] == "123 F.3d 456"
    assert case["treatment"]["warning_level"] == "none" and case["treatment"]["status"] == "checked"
    checker.assert_awaited_once_with("123 F.3d 456")

    v = result["verification"]
    assert v == {
        "quotes_verified": 2,
        "quotes_unverified": 1,
        "invalid_refs_stripped": 2 + 1 + 1,  # q1 (2) + synthesis answer [P9] + finding P9
        "case_references_removed": [],
        "citations_checked": 1,
    }
    scope = result["scope"]
    assert (
        scope["tool_calls"] == 2
        and scope["passages_collected"] == 2
        and scope["passages_cited"] == 2
    )
    assert scope["case_law_searched"] is True and scope["documents_in_scope"] == 1
    assert result["notice"].startswith("AI-generated")


@pytest.mark.asyncio
async def test_runner_honours_the_concurrency_cap_and_continues_past_a_failed_step():
    active = 0
    peak = 0

    async def slow_search(query, **kwargs):
        nonlocal active, peak
        active += 1
        peak = max(peak, active)
        await asyncio.sleep(0.02)
        active -= 1
        return [_doc_hit("d1", 0, DOC_TEXT)]

    plan = [{"id": f"q{i}", "question": f"Question {i}?", "tools": ["documents"]} for i in range(5)]

    async def chat(client, **kwargs):
        prompt = kwargs["messages"][0]["content"]
        if "SUB-QUESTION: Question 2?" in prompt:
            raise RuntimeError("model hiccup")
        if "SUB-QUESTION:" in prompt:
            return _response({"answer": "Fine [P1].", "claims": [], "followup": ""})
        return _response({"answer": "Overall fine [P1].", "key_findings": [], "open_questions": []})

    with (
        patch.object(pipeline, "openai_chat", AsyncMock(side_effect=chat)),
        patch.object(pipeline, "search_firm_documents", AsyncMock(side_effect=slow_search)),
    ):
        result = await ResearchRunner(MagicMock(), _config(concurrency=2)).run("Q?", plan)

    assert peak <= 2
    statuses = [s["status"] for s in result["sub_questions"]]
    assert statuses.count("failed") == 1 and statuses.count("completed") == 4
    failed = next(s for s in result["sub_questions"] if s["status"] == "failed")
    assert failed["error"].startswith("RuntimeError") and result["scope"]["steps_failed"] == 1
    assert result["answer"] == "Overall fine [P1]."


@pytest.mark.asyncio
async def test_follow_up_runs_once_and_the_tool_budget_is_enforced():
    calls: list[str] = []

    async def search(query, **kwargs):
        calls.append(query)
        return [_doc_hit("d1", len(calls), f"Passage about {query}.")]

    answers_seen: list[bool] = []

    async def chat(client, **kwargs):
        prompt = kwargs["messages"][0]["content"]
        if "SUB-QUESTION:" in prompt:
            allow = "ONE further search query" in prompt
            answers_seen.append(allow)
            return _response(
                {
                    "answer": "Partial [P1].",
                    "claims": [],
                    "followup": "narrower query" if allow else "should be ignored",
                }
            )
        return _response({"answer": "Done [P1].", "key_findings": [], "open_questions": []})

    plan = [{"id": "q1", "question": "Broad question?", "tools": ["documents"]}]
    with (
        patch.object(pipeline, "openai_chat", AsyncMock(side_effect=chat)),
        patch.object(pipeline, "search_firm_documents", AsyncMock(side_effect=search)),
    ):
        result = await ResearchRunner(MagicMock(), _config(max_tool_calls=24)).run("Q?", plan)
    step = result["sub_questions"][0]
    assert calls == ["Broad question?", "narrower query"]
    assert step["followup_query"] == "narrower query" and step["passages_found"] == 2
    assert answers_seen == [True, False]  # the re-written answer may not propose another follow-up
    assert result["scope"]["tool_calls"] == 2

    # Budget of one call: the follow-up is refused and recorded, never silently skipped.
    calls.clear()
    answers_seen.clear()
    with (
        patch.object(pipeline, "openai_chat", AsyncMock(side_effect=chat)),
        patch.object(pipeline, "search_firm_documents", AsyncMock(side_effect=search)),
    ):
        result = await ResearchRunner(MagicMock(), _config(max_tool_calls=1)).run("Q?", plan)
    step = result["sub_questions"][0]
    assert calls == ["Broad question?"]
    assert answers_seen == [False]  # no budget left: the answer call is told not to propose one
    assert step["followup_query"] is None
    assert result["scope"]["tool_calls"] == 1 and result["scope"]["tool_call_cap"] == 1

    # With depth 0 no follow-up is ever asked for.
    with (
        patch.object(pipeline, "openai_chat", AsyncMock(side_effect=chat)),
        patch.object(pipeline, "search_firm_documents", AsyncMock(side_effect=search)),
    ):
        result = await ResearchRunner(MagicMock(), _config(followup_depth=0)).run("Q?", plan)
    assert result["sub_questions"][0]["followup_query"] is None


@pytest.mark.asyncio
async def test_tool_failures_are_recorded_and_synthesis_failure_is_fatal():
    plan = [{"id": "q1", "question": "Q1?", "tools": ["documents", "case_law"]}]

    async def chat(client, **kwargs):
        prompt = kwargs["messages"][0]["content"]
        if "SUB-QUESTION:" in prompt:
            return _response({"answer": "Nothing found.", "claims": [], "followup": ""})
        raise RuntimeError("synthesis down")

    with (
        patch.object(pipeline, "openai_chat", AsyncMock(side_effect=chat)),
        patch.object(
            pipeline, "search_firm_documents", AsyncMock(side_effect=ConnectionError("index down"))
        ),
        patch.object(pipeline, "search_case_law", AsyncMock(side_effect=RuntimeError("429"))),
    ):
        runner = ResearchRunner(MagicMock(), _config())
        with pytest.raises(SynthesisError):
            await runner.run("Q?", plan)
    step = runner.steps[0]
    assert step.status == "completed" and step.passages_found == 0
    assert [c["tool"] for c in step.tool_calls] == ["documents", "case_law"]
    assert all("error" in c for c in step.tool_calls)
    assert step.tool_calls[0]["error"].startswith("ConnectionError")
