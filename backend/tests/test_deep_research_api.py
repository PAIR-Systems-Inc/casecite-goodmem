"""HTTP tests for deep research: the synchronous plan, the run as a background
job (202, poll, persisted result), owner / member / stranger rules (404, never
403), the document scope, cancellation mid-run and a failed synthesis.
"""

import os

os.environ.setdefault("SECRET_KEY", "test-secret-key-for-testing-only-32chars!")
os.environ.setdefault("ENCRYPTION_SALT", "test-salt-16chars!")
os.environ.setdefault("DEBUG", "true")

import asyncio
import json
import time
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from app.services.deep_research import pipeline
from app.services.deep_research.service import DeepResearchService

from tests.helpers import headers, make_shared_matter, make_user, register_document

DOC_TEXT = (
    "LIMITATION OF LIABILITY. The aggregate liability of Supplier is capped at the fees "
    "paid in the preceding twelve months."
)
CASE_TEXT = (
    "A limitation of liability clause is enforceable between sophisticated commercial parties."
)
QUESTION = "Is the liability cap in the MSA enforceable?"
PLAN = [
    {"id": "q1", "question": "What does the MSA say about liability?", "tools": ["documents"]},
    {"id": "q2", "question": "Are liability caps enforceable?", "tools": ["case_law"]},
]


@pytest.fixture(autouse=True)
def _no_rate_limit():
    with patch(
        "app.middleware.security.RateLimiter.is_allowed",
        return_value=(True, {"limit": 100, "remaining": 99}),
    ):
        yield


@pytest.fixture(autouse=True)
def _fake_client():
    with patch.object(DeepResearchService, "_llm_client", return_value=MagicMock()):
        yield


def _response(payload: dict) -> MagicMock:
    response = MagicMock()
    response.choices = [MagicMock()]
    response.choices[0].message.content = json.dumps(payload)
    return response


def _doc_hit(doc_id: str) -> dict:
    return {
        "id": f"{doc_id}-0",
        "text": DOC_TEXT,
        "metadata": {"document_id": doc_id, "chunk_index": 0, "filename": "msa.pdf"},
        "similarity": 0.9,
    }


def _case_hit() -> dict:
    return {
        "case_name": "Acme v. Beta",
        "citation": "123 F.3d 456",
        "opinion_id": "77",
        "cluster_id": "9",
        "url": "https://www.courtlistener.com/opinion/77/",
        "court": "ca2",
        "date_filed": "2020-01-01",
        "text": CASE_TEXT,
        "excerpt_source": "opinion",
        "score": 10.0,
    }


async def _model(client, **kwargs):
    prompt = kwargs["messages"][0]["content"]
    if "SUB-QUESTION: What does the MSA say" in prompt:
        return _response(
            {
                "answer": "Liability is capped at twelve months of fees [P1].",
                "claims": [
                    {
                        "text": "The cap is twelve months of fees.",
                        "refs": ["P1"],
                        "quote": "capped at the fees paid in the preceding twelve months",
                    }
                ],
                "followup": "",
            }
        )
    if "SUB-QUESTION:" in prompt:
        return _response(
            {
                "answer": "Yes [P2].",
                "claims": [
                    {
                        "text": "Caps bind sophisticated parties.",
                        "refs": ["P2"],
                        "quote": "enforceable between sophisticated commercial parties",
                    }
                ],
                "followup": "",
            }
        )
    return _response(
        {
            "answer": "The cap is enforceable [P1, P2].",
            "key_findings": [{"text": "The cap is twelve months of fees.", "refs": ["P1"]}],
            "open_questions": [],
        }
    )


def _run_patches(search_docs=None, search_cases=None, model=None):
    return (
        patch.object(pipeline, "openai_chat", AsyncMock(side_effect=model or _model)),
        patch.object(
            pipeline,
            "search_firm_documents",
            search_docs or AsyncMock(return_value=[_doc_hit("d1")]),
        ),
        patch.object(
            pipeline, "search_case_law", search_cases or AsyncMock(return_value=[_case_hit()])
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
        ),
    )


def _poll(client, user, job_id: str, attempts: int = 200) -> dict:
    for _ in range(attempts):
        job = client.get(f"/api/v1/jobs/{job_id}", headers=headers(user))
        assert job.status_code == 200, job.text
        if job.json()["status"] in ("completed", "failed", "cancelled"):
            return job.json()
        time.sleep(0.05)
    raise AssertionError("job did not settle")


def _start(client, user, body: dict | None = None) -> dict:
    payload = {"question": QUESTION, "plan": PLAN}
    payload.update(body or {})
    resp = client.post("/api/v1/deep-research/run", headers=headers(user), json=payload)
    assert resp.status_code == 202, resp.text
    return resp.json()


def _run_to_completion(client, user, body: dict | None = None, **patches):
    model_patch, docs_patch, cases_patch, check_patch = _run_patches(**patches)
    with model_patch, docs_patch, cases_patch, check_patch:
        started = _start(client, user, body)
        job = _poll(client, user, started["job_id"])
    return started, job


# ---------------------------------------------------------------------- plan


def test_plan_returns_sub_questions_scope_and_caps(client):
    owner = make_user("owner")
    register_document(owner, filename="msa.pdf")
    register_document(owner, filename="nda.pdf")
    proposed = _response(
        {
            "sub_questions": [
                {"question": "What does the MSA say about liability?", "tools": ["documents"]},
                {"question": "Are liability caps enforceable?", "tools": ["case_law", "nonsense"]},
            ]
        }
    )
    with patch(
        "app.services.deep_research.planner.openai_chat", AsyncMock(return_value=proposed)
    ) as chat:
        resp = client.post(
            "/api/v1/deep-research/plan", headers=headers(owner), json={"question": QUESTION}
        )
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["question"] == QUESTION
    assert [s["id"] for s in body["plan"]] == ["q1", "q2"]
    assert body["plan"][1]["tools"] == ["case_law"]
    assert body["scope"] == {
        "matter_id": None,
        "document_ids": None,
        "documents_in_scope": 2,
        "case_law_available": True,
        "citation_check_available": body["scope"]["citation_check_available"],
    }
    assert body["caps"]["max_subquestions"] >= 1 and body["model"]
    # The planner reads the question inside the untrusted delimiters only.
    prompt = chat.await_args.kwargs["messages"][0]["content"]
    assert QUESTION in prompt and "<<<BEGIN DOCUMENT" in prompt


def test_plan_scopes_to_the_matter_or_an_explicit_pick_and_refuses_foreign_matters(client):
    owner, member, stranger = make_user("owner"), make_user("member"), make_user("stranger")
    matter_id = make_shared_matter(owner, member)
    filed = register_document(owner, filename="filed.pdf", matter_id=matter_id)
    private = register_document(owner, filename="private.pdf")
    proposed = _response(
        {"sub_questions": [{"question": "What is in the file?", "tools": ["documents"]}]}
    )

    with patch("app.services.deep_research.planner.openai_chat", AsyncMock(return_value=proposed)):
        as_member = client.post(
            "/api/v1/deep-research/plan",
            headers=headers(member),
            json={"question": QUESTION, "matter_id": matter_id},
        )
        assert as_member.status_code == 200, as_member.text
        assert as_member.json()["scope"]["document_ids"] == [filed]
        assert as_member.json()["scope"]["documents_in_scope"] == 1

        picked = client.post(
            "/api/v1/deep-research/plan",
            headers=headers(owner),
            json={"question": QUESTION, "document_filter": {"document_ids": [private, filed]}},
        )
        assert picked.status_code == 200
        assert sorted(picked.json()["scope"]["document_ids"]) == sorted([private, filed])

        # A member picking the owner's private document gets nothing from it.
        picked = client.post(
            "/api/v1/deep-research/plan",
            headers=headers(member),
            json={"question": QUESTION, "document_filter": {"document_ids": [private]}},
        )
        assert picked.status_code == 200 and picked.json()["scope"]["document_ids"] == []

        refused = client.post(
            "/api/v1/deep-research/plan",
            headers=headers(stranger),
            json={"question": QUESTION, "matter_id": matter_id},
        )
        assert refused.status_code == 404


def test_plan_reports_a_missing_key_as_400_and_a_planner_failure_as_502(client):
    owner = make_user("owner")
    with patch.object(DeepResearchService, "_llm_client", side_effect=ValueError("No key")):
        resp = client.post(
            "/api/v1/deep-research/plan", headers=headers(owner), json={"question": QUESTION}
        )
        assert resp.status_code == 400 and "No key" in resp.text
    with patch(
        "app.services.deep_research.planner.openai_chat",
        AsyncMock(side_effect=RuntimeError("down")),
    ):
        resp = client.post(
            "/api/v1/deep-research/plan", headers=headers(owner), json={"question": QUESTION}
        )
        assert resp.status_code == 502


# ----------------------------------------------------------------------- run


def test_run_completes_persists_the_result_and_audits_counts_only(client, audit_events):
    owner = make_user("owner")
    register_document(owner, filename="msa.pdf")
    started, job = _run_to_completion(client, owner)
    assert started["sub_questions"] == 2 and started["poll_url"].endswith(started["job_id"])
    assert job["status"] == "completed", job
    result = job["result"]
    assert result["answer"] == "The cap is enforceable [P1, P2]."
    assert [s["ref"] for s in result["sources"]] == ["P1", "P2"]
    assert result["verification"]["quotes_verified"] == 2
    assert result["run_id"] == started["run_id"]

    detail = client.get(f"/api/v1/deep-research/{started['run_id']}", headers=headers(owner))
    assert detail.status_code == 200, detail.text
    body = detail.json()
    assert body["status"] == "completed" and body["role"] == "owner"
    assert body["job_id"] == started["job_id"] and body["completed_at"]
    assert body["plan"] == PLAN and body["sources_cited"] == 2
    assert body["result"]["answer"] == result["answer"]
    assert body["scope"]["document_ids"] is None and body["scope"]["documents_in_scope"] == 1
    assert body["result"]["scope"]["matter_id"] is None

    listed = client.get("/api/v1/deep-research", headers=headers(owner))
    assert [r["id"] for r in listed.json()["runs"]] == [started["run_id"]]
    assert listed.json()["runs"][0]["status"] == "completed"

    actions = [e["details"].get("action") for e in audit_events]
    assert "deep_research_started" in actions and "deep_research_completed" in actions
    for event in audit_events:
        assert QUESTION not in json.dumps(event["details"])
        assert "twelve months" not in json.dumps(event["details"])
    completed = next(e for e in audit_events if e["details"]["action"] == "deep_research_completed")
    assert (
        completed["details"]["sources_cited"] == 2 and completed["details"]["quotes_verified"] == 2
    )
    started_event = next(
        e for e in audit_events if e["details"]["action"] == "deep_research_started"
    )
    assert started_event["details"]["tools"] == ["case_law", "documents"]


def test_run_in_a_matter_searches_only_its_documents_and_is_readable_by_members(client):
    owner, member, stranger = make_user("owner"), make_user("member"), make_user("stranger")
    matter_id = make_shared_matter(owner, member)
    filed = register_document(owner, filename="filed.pdf", matter_id=matter_id)
    register_document(owner, filename="private.pdf")

    search = AsyncMock(return_value=[_doc_hit(filed)])
    started, job = _run_to_completion(client, owner, {"matter_id": matter_id}, search_docs=search)
    assert job["status"] == "completed"
    # The firm-document tool was scoped to the matter's documents and the member set.
    kwargs = search.await_args.kwargs
    assert kwargs["document_ids"] == [filed] and kwargs["user_id"] == owner.id
    assert matter_id in (kwargs["matter_ids"] or [])

    run_id = started["run_id"]
    as_member = client.get(f"/api/v1/deep-research/{run_id}", headers=headers(member))
    assert as_member.status_code == 200 and as_member.json()["role"] == "member"
    assert as_member.json()["scope"]["document_ids"] == [filed]
    assert as_member.json()["result"]["scope"]["matter_id"] == matter_id
    listed = client.get(f"/api/v1/deep-research?matter_id={matter_id}", headers=headers(member))
    assert [r["id"] for r in listed.json()["runs"]] == [run_id]

    assert (
        client.get(f"/api/v1/deep-research/{run_id}", headers=headers(stranger)).status_code == 404
    )
    assert client.get("/api/v1/deep-research", headers=headers(stranger)).json()["runs"] == []
    assert (
        client.get(
            f"/api/v1/deep-research?matter_id={matter_id}", headers=headers(stranger)
        ).status_code
        == 404
    )

    # A stranger cannot start a run in the matter either.
    with patch.object(pipeline, "openai_chat", AsyncMock(side_effect=_model)):
        resp = client.post(
            "/api/v1/deep-research/run",
            headers=headers(stranger),
            json={"question": QUESTION, "plan": PLAN, "matter_id": matter_id},
        )
    assert resp.status_code == 404


def test_delete_is_owner_only(client, audit_events):
    owner, member, stranger = make_user("owner"), make_user("member"), make_user("stranger")
    matter_id = make_shared_matter(owner, member)
    register_document(owner, matter_id=matter_id)
    started, _job = _run_to_completion(client, owner, {"matter_id": matter_id})
    run_id = started["run_id"]
    for user in (member, stranger):
        assert (
            client.delete(f"/api/v1/deep-research/{run_id}", headers=headers(user)).status_code
            == 404
        )
    assert client.get(f"/api/v1/deep-research/{run_id}", headers=headers(member)).status_code == 200
    resp = client.delete(f"/api/v1/deep-research/{run_id}", headers=headers(owner))
    assert resp.status_code == 200 and resp.json() == {"status": "deleted", "id": run_id}
    assert client.get(f"/api/v1/deep-research/{run_id}", headers=headers(owner)).status_code == 404
    assert any(e["details"].get("action") == "deep_research_deleted" for e in audit_events)


def test_run_validates_the_plan_and_requires_a_model(client):
    owner = make_user("owner")
    too_many = [
        {"id": f"q{i}", "question": f"Question {i}?", "tools": ["documents"]} for i in range(9)
    ]
    resp = client.post(
        "/api/v1/deep-research/run",
        headers=headers(owner),
        json={"question": QUESTION, "plan": too_many},
    )
    assert resp.status_code == 400 and "at most 8" in resp.text
    dup = [PLAN[0], PLAN[0]]
    resp = client.post(
        "/api/v1/deep-research/run",
        headers=headers(owner),
        json={"question": QUESTION, "plan": dup},
    )
    assert resp.status_code == 422
    bad_tool = [{"id": "q1", "question": "What?", "tools": ["web"]}]
    resp = client.post(
        "/api/v1/deep-research/run",
        headers=headers(owner),
        json={"question": QUESTION, "plan": bad_tool},
    )
    assert resp.status_code == 422
    with patch.object(DeepResearchService, "_llm_client", side_effect=ValueError("No key")):
        resp = client.post(
            "/api/v1/deep-research/run",
            headers=headers(owner),
            json={"question": QUESTION, "plan": PLAN},
        )
        assert resp.status_code == 400 and "No key" in resp.text
    assert client.get("/api/v1/deep-research", headers=headers(owner)).json()["runs"] == []


def test_cancelling_the_job_marks_the_run_cancelled(client):
    owner = make_user("owner")
    register_document(owner)
    release = asyncio.Event()
    entered = asyncio.Event()

    async def blocking_search(query, **kwargs):
        entered.set()
        await release.wait()
        return []

    model_patch, docs_patch, cases_patch, check_patch = _run_patches(
        search_docs=AsyncMock(side_effect=blocking_search)
    )
    with model_patch, docs_patch, cases_patch, check_patch:
        started = _start(client, owner, {"plan": [PLAN[0]]})
        for _ in range(100):
            if entered.is_set():
                break
            time.sleep(0.02)
        assert entered.is_set(), "the run never reached its first tool call"
        cancelled = client.delete(f"/api/v1/jobs/{started['job_id']}", headers=headers(owner))
        assert cancelled.status_code == 200, cancelled.text
        job = _poll(client, owner, started["job_id"])
        assert job["status"] == "cancelled"
        for _ in range(100):
            detail = client.get(
                f"/api/v1/deep-research/{started['run_id']}", headers=headers(owner)
            ).json()
            if detail["status"] == "cancelled":
                break
            time.sleep(0.02)
    assert detail["status"] == "cancelled" and detail["result"] is None
    assert detail["error"] == "Cancelled before completion"


def test_a_failed_synthesis_fails_the_job_and_records_the_error(client):
    owner = make_user("owner")
    register_document(owner)

    async def broken_model(client_, **kwargs):
        prompt = kwargs["messages"][0]["content"]
        if "SUB-QUESTION:" in prompt:
            return _response({"answer": "Fine [P1].", "claims": [], "followup": ""})
        raise RuntimeError("synthesis down")

    started, job = _run_to_completion(client, owner, {"plan": [PLAN[0]]}, model=broken_model)
    assert job["status"] == "failed"
    assert "synthesis could not be completed" in (job["error"] or "")
    detail = client.get(f"/api/v1/deep-research/{started['run_id']}", headers=headers(owner)).json()
    assert detail["status"] == "failed" and detail["result"] is None
    assert "synthesis could not be completed" in detail["error"]
