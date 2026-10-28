"""HTTP tests for workflows: CRUD and its owner / member / stranger rules (404,
never 403), templates, a full run through the job queue with stubbed step
adapters, a failure that stops the run and a resume that continues from the
failed step with earlier outputs reloaded, cancellation, and one real step
(extract_fields) producing a review table.
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
from app.services.review_tables import review_table_service
from app.services.review_tables.service import ReviewTableService
from app.services.workflows import service as service_module
from app.services.workflows.service import WorkflowService
from app.services.workflows.steps import StepError, StepOutput

from tests.helpers import headers, make_shared_matter, make_user, register_document

FIELDS = [{"name": "Governing law", "type": "text"}]
STEPS = [
    {"name": "extract", "kind": "extract_fields", "params": {"fields": FIELDS}},
    {"name": "review", "kind": "analyze_contract", "params": {}},
    {"name": "cites", "kind": "citation_check", "params": {"from_steps": ["review"]}},
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
    with patch.object(WorkflowService, "_llm_client", return_value=MagicMock()):
        yield


def _create(client, user, *, steps=STEPS, matter_id=None, name="Intake"):
    body = {"name": name, "steps": steps}
    if matter_id:
        body["matter_id"] = matter_id
    resp = client.post("/api/v1/workflows", headers=headers(user), json=body)
    assert resp.status_code == 201, resp.text
    return resp.json()


def _poll(client, user, job_id: str, attempts: int = 200) -> dict:
    for _ in range(attempts):
        job = client.get(f"/api/v1/jobs/{job_id}", headers=headers(user))
        assert job.status_code == 200, job.text
        if job.json()["status"] in ("completed", "failed", "cancelled"):
            return job.json()
        time.sleep(0.05)
    raise AssertionError("job did not settle")


def _fake_adapter(name: str, fail: bool = False, calls: list | None = None):
    async def adapter(step, ctx):
        if calls is not None:
            calls.append(
                {
                    "step": step["name"],
                    "inputs": list(ctx.input_document_ids),
                    "outputs": dict(ctx.outputs),
                }
            )
        if fail:
            raise StepError(f"{name} broke")
        return StepOutput(
            summary=f"{name} done",
            data={"from": name},
            document_ids=list(ctx.input_document_ids),
            produced={"analyses": [f"{name}-1"]},
        )

    return adapter


def _run(client, user, wid: str, document_ids=None):
    resp = client.post(
        f"/api/v1/workflows/{wid}/run",
        headers=headers(user),
        json={"document_ids": document_ids or []},
    )
    assert resp.status_code == 202, resp.text
    return resp.json()


# ------------------------------------------------------------------ create / read


def test_create_lists_and_reads_a_workflow_with_normalised_steps(client, audit_events):
    owner = make_user("owner")
    wf = _create(client, owner)
    assert wf["role"] == "owner" and wf["step_count"] == 3 and wf["run_count"] == 0
    assert wf["step_kinds"] == ["extract_fields", "analyze_contract", "citation_check"]
    assert wf["steps"][0]["params"]["fields"][0]["key"] == "governing_law"
    assert wf["steps"][0]["inputs"] == {"source": "run", "document_ids": [], "step": None}
    assert wf["steps"][1]["params"]["posture"] == "balanced"
    assert any(e["details"].get("action") == "workflow_created" for e in audit_events)
    for event in audit_events:
        assert "Governing law" not in json.dumps(event["details"])

    listed = client.get("/api/v1/workflows", headers=headers(owner))
    assert [w["id"] for w in listed.json()["workflows"]] == [wf["id"]]
    detail = client.get(f"/api/v1/workflows/{wf['id']}", headers=headers(owner))
    assert detail.status_code == 200 and detail.json()["steps"] == wf["steps"]


def test_create_rejects_bad_steps_foreign_documents_and_too_many_steps(client):
    owner, other = make_user("owner"), make_user("other")
    theirs = register_document(other)
    bad = [
        {"name": "a", "kind": "citation_check", "params": {"from_steps": ["later"]}},
        {"name": "later", "kind": "analyze_contract"},
    ]
    resp = client.post(
        "/api/v1/workflows", headers=headers(owner), json={"name": "x", "steps": bad}
    )
    assert resp.status_code == 422 and "not an earlier step" in resp.text
    resp = client.post(
        "/api/v1/workflows",
        headers=headers(owner),
        json={"name": "x", "steps": [{"name": "a", "kind": "nope"}]},
    )
    assert resp.status_code == 422
    fixed = [
        {
            "name": "a",
            "kind": "analyze_contract",
            "inputs": {"source": "documents", "document_ids": [theirs]},
        }
    ]
    resp = client.post(
        "/api/v1/workflows", headers=headers(owner), json={"name": "x", "steps": fixed}
    )
    assert resp.status_code == 404  # someone else's private document: no existence leak
    many = [{"name": f"s{i}", "kind": "analyze_contract"} for i in range(13)]
    resp = client.post(
        "/api/v1/workflows", headers=headers(owner), json={"name": "x", "steps": many}
    )
    assert resp.status_code == 422 and "at most 12 steps" in resp.text
    stranger_matter = make_shared_matter(other)
    resp = client.post(
        "/api/v1/workflows",
        headers=headers(owner),
        json={"name": "x", "steps": STEPS, "matter_id": stranger_matter},
    )
    assert resp.status_code == 404


def test_member_reads_but_only_the_owner_edits_or_deletes(client, audit_events):
    owner, member, stranger = make_user("owner"), make_user("member"), make_user("stranger")
    matter_id = make_shared_matter(owner, member)
    wf = _create(client, owner, matter_id=matter_id)
    wid = wf["id"]

    as_member = client.get(f"/api/v1/workflows/{wid}", headers=headers(member))
    assert as_member.status_code == 200 and as_member.json()["role"] == "member"
    assert [
        w["id"]
        for w in client.get("/api/v1/workflows", headers=headers(member)).json()["workflows"]
    ] == [wid]
    assert client.get(f"/api/v1/workflows/{wid}", headers=headers(stranger)).status_code == 404
    assert client.get("/api/v1/workflows", headers=headers(stranger)).json()["workflows"] == []

    for user in (member, stranger):
        assert (
            client.patch(
                f"/api/v1/workflows/{wid}", headers=headers(user), json={"name": "Hijack"}
            ).status_code
            == 404
        )
        assert client.delete(f"/api/v1/workflows/{wid}", headers=headers(user)).status_code == 404
    assert (
        client.patch(f"/api/v1/workflows/{wid}", headers=headers(owner), json={}).status_code == 400
    )

    renamed = client.patch(
        f"/api/v1/workflows/{wid}",
        headers=headers(owner),
        json={"name": "Renamed", "steps": [{"name": "only", "kind": "analyze_contract"}]},
    )
    assert renamed.status_code == 200, renamed.text
    assert renamed.json()["name"] == "Renamed" and renamed.json()["step_count"] == 1
    assert any(e["details"].get("action") == "workflow_updated" for e in audit_events)

    deleted = client.delete(f"/api/v1/workflows/{wid}", headers=headers(owner))
    assert deleted.status_code == 200 and deleted.json() == {"status": "deleted", "id": wid}
    assert client.get(f"/api/v1/workflows/{wid}", headers=headers(owner)).status_code == 404
    assert any(e["details"].get("action") == "workflow_deleted" for e in audit_events)


# ------------------------------------------------------------------- templates


def test_templates_are_listed_and_instantiated_for_the_caller(client, audit_events):
    owner, member = make_user("owner"), make_user("member")
    matter_id = make_shared_matter(owner, member)
    listed = client.get("/api/v1/workflows/templates", headers=headers(owner))
    assert listed.status_code == 200, listed.text
    templates = listed.json()["templates"]
    assert [t["key"] for t in templates] == ["nda_intake", "lease_review", "due_diligence"]
    assert templates[0]["steps"][0]["params"]["fields"][0]["key"] == "counterparty"

    created = client.post(
        f"/api/v1/workflows/from-template/lease_review?matter_id={matter_id}",
        headers=headers(member),
    )
    assert created.status_code == 201, created.text
    body = created.json()
    assert body["template_key"] == "lease_review" and body["matter_id"] == matter_id
    assert body["step_kinds"] == ["extract_fields", "compare_contracts", "draft_document"]
    assert body["owner_id"] == member.id and body["role"] == "owner"
    # The owner of the matter sees the member's workflow through the matter.
    assert (
        client.get(f"/api/v1/workflows/{body['id']}", headers=headers(owner)).json()["role"]
        == "member"
    )
    event = next(e for e in audit_events if e["details"].get("action") == "workflow_created")
    assert event["details"]["template"] == "lease_review"

    assert (
        client.post("/api/v1/workflows/from-template/nope", headers=headers(owner)).status_code
        == 404
    )
    stranger = make_user("stranger")
    assert (
        client.post(
            f"/api/v1/workflows/from-template/nda_intake?matter_id={matter_id}",
            headers=headers(stranger),
        ).status_code
        == 404
    )


# ------------------------------------------------------------------------ runs


def test_run_executes_every_step_in_order_and_persists_outputs(client, audit_events):
    owner = make_user("owner")
    d1 = register_document(owner, filename="a.pdf")
    d2 = register_document(owner, filename="b.pdf")
    wf = _create(client, owner)
    calls: list = []
    fakes = {
        "extract_fields": _fake_adapter("extract", calls=calls),
        "analyze_contract": _fake_adapter("review", calls=calls),
        "citation_check": _fake_adapter("cites", calls=calls),
    }
    with patch.dict(service_module.ADAPTERS, fakes):
        started = _run(client, owner, wf["id"], [d1, d2, d1])
        job = _poll(client, owner, started["job_id"])
    assert started["steps"] == 3 and started["documents"] == 2
    assert job["status"] == "completed", job
    assert job["result"]["completed_steps"] == 3 and job["result"]["produced"] == {"analyses": 3}

    # Steps ran in order, each seeing the outputs of the ones before it.
    assert [c["step"] for c in calls] == ["extract", "review", "cites"]
    assert calls[0]["inputs"] == [d1, d2] and calls[0]["outputs"] == {}
    assert sorted(calls[2]["outputs"]) == ["extract", "review"]
    assert calls[2]["outputs"]["review"]["summary"] == "review done"

    detail = client.get(
        f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}", headers=headers(owner)
    )
    assert detail.status_code == 200, detail.text
    run = detail.json()
    assert run["status"] == "completed" and run["completed_steps"] == 3 and run["documents"] == 2
    assert run["input_document_ids"] == [d1, d2] and run["job_id"] == started["job_id"]
    assert [s["status"] for s in run["step_runs"]] == ["completed"] * 3
    assert run["step_runs"][1]["output"]["summary"] == "review done"
    assert run["step_runs"][1]["produced_ids"] == {"analyses": ["review-1"]}
    assert run["step_runs"][1]["input_document_ids"] == [d1, d2]
    assert run["step_runs"][0]["started_at"] and run["step_runs"][0]["finished_at"]
    assert [s["name"] for s in run["step_definitions"]] == ["extract", "review", "cites"]

    runs = client.get(f"/api/v1/workflows/{wf['id']}/runs", headers=headers(owner)).json()["runs"]
    assert [r["id"] for r in runs] == [started["run_id"]] and runs[0]["completed_steps"] == 3
    summary = client.get(f"/api/v1/workflows/{wf['id']}", headers=headers(owner)).json()
    assert summary["run_count"] == 1 and summary["last_run_status"] == "completed"

    actions = [e["details"].get("action") for e in audit_events]
    assert "workflow_run_started" in actions and "workflow_run_completed" in actions
    started_event = next(
        e for e in audit_events if e["details"]["action"] == "workflow_run_started"
    )
    assert (
        started_event["details"]["documents"] == 2 and started_event["details"]["start_index"] == 0
    )
    for event in audit_events:
        assert "a.pdf" not in json.dumps(event["details"])


def test_a_failed_step_stops_the_run_and_resume_continues_from_it(client, audit_events):
    owner = make_user("owner")
    d1 = register_document(owner)
    wf = _create(client, owner)
    calls: list = []
    with patch.dict(
        service_module.ADAPTERS,
        {
            "extract_fields": _fake_adapter("extract", calls=calls),
            "analyze_contract": _fake_adapter("review", fail=True, calls=calls),
            "citation_check": _fake_adapter("cites", calls=calls),
        },
    ):
        started = _run(client, owner, wf["id"], [d1])
        job = _poll(client, owner, started["job_id"])
    assert job["status"] == "failed" and "review broke" in (job["error"] or "")
    run_url = f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}"
    run = client.get(run_url, headers=headers(owner)).json()
    assert run["status"] == "failed" and run["completed_steps"] == 1
    assert "Step 2/3 'review' failed: review broke" in run["error"]
    assert [s["status"] for s in run["step_runs"]] == ["completed", "failed", "skipped"]
    assert run["step_runs"][1]["error"] == "review broke"
    assert any(e["details"].get("action") == "workflow_run_failed" for e in audit_events)
    assert [c["step"] for c in calls] == ["extract", "review"]

    # Resume: the fixed step runs with the first step's output reloaded; nothing re-runs.
    calls.clear()
    with patch.dict(
        service_module.ADAPTERS,
        {
            "extract_fields": _fake_adapter("extract", calls=calls),
            "analyze_contract": _fake_adapter("review", calls=calls),
            "citation_check": _fake_adapter("cites", calls=calls),
        },
    ):
        resumed = client.post(f"{run_url}/resume", headers=headers(owner))
        assert resumed.status_code == 202, resumed.text
        assert resumed.json()["run_id"] == started["run_id"]
        job = _poll(client, owner, resumed.json()["job_id"])
    assert job["status"] == "completed", job
    assert [c["step"] for c in calls] == ["review", "cites"]
    assert calls[0]["outputs"]["extract"]["summary"] == "extract done"
    run = client.get(run_url, headers=headers(owner)).json()
    assert run["status"] == "completed" and run["resumed_count"] == 1 and run["error"] is None
    assert [s["status"] for s in run["step_runs"]] == ["completed"] * 3
    assert run["job_id"] == resumed.json()["job_id"]
    assert any(e["details"].get("action") == "workflow_run_resumed" for e in audit_events)

    # A completed run has nothing to resume.
    again = client.post(f"{run_url}/resume", headers=headers(owner))
    assert again.status_code == 400


def test_run_requires_documents_a_model_and_readable_inputs(client):
    owner, other = make_user("owner"), make_user("other")
    theirs = register_document(other)
    mine = register_document(owner)
    wf = _create(client, owner)
    resp = client.post(
        f"/api/v1/workflows/{wf['id']}/run", headers=headers(owner), json={"document_ids": []}
    )
    assert resp.status_code == 400 and "at least one document" in resp.text
    resp = client.post(
        f"/api/v1/workflows/{wf['id']}/run", headers=headers(owner), json={"document_ids": [theirs]}
    )
    assert resp.status_code == 404
    with patch.object(WorkflowService, "_llm_client", side_effect=ValueError("No key")):
        resp = client.post(
            f"/api/v1/workflows/{wf['id']}/run",
            headers=headers(owner),
            json={"document_ids": [mine]},
        )
        assert resp.status_code == 400 and "No key" in resp.text
    assert (
        client.get(f"/api/v1/workflows/{wf['id']}/runs", headers=headers(owner)).json()["runs"]
        == []
    )
    assert (
        client.get(f"/api/v1/workflows/{wf['id']}/runs/nope", headers=headers(owner)).status_code
        == 404
    )


def test_member_runs_a_shared_workflow_over_the_matter_documents_but_a_stranger_cannot(client):
    owner, member, stranger = make_user("owner"), make_user("member"), make_user("stranger")
    matter_id = make_shared_matter(owner, member)
    filed = register_document(owner, filename="filed.pdf", matter_id=matter_id)
    register_document(owner, filename="private.pdf")
    wf = _create(client, owner, matter_id=matter_id)
    calls: list = []
    fakes = {
        kind: _fake_adapter(kind, calls=calls)
        for kind in ("extract_fields", "analyze_contract", "citation_check")
    }
    with patch.dict(service_module.ADAPTERS, fakes):
        started = _run(client, member, wf["id"])  # no ids: the matter's documents
        job = _poll(client, member, started["job_id"])
    assert job["status"] == "completed" and started["documents"] == 1
    assert calls[0]["inputs"] == [filed]
    run = client.get(
        f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}", headers=headers(owner)
    ).json()
    assert run["runner_id"] == member.id and run["matter_id"] == matter_id
    assert run["input_document_ids"] == [filed]

    assert (
        client.post(
            f"/api/v1/workflows/{wf['id']}/run", headers=headers(stranger), json={}
        ).status_code
        == 404
    )
    assert (
        client.get(f"/api/v1/workflows/{wf['id']}/runs", headers=headers(stranger)).status_code
        == 404
    )
    assert (
        client.post(
            f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}/resume",
            headers=headers(stranger),
        ).status_code
        == 404
    )


def test_cancelling_the_job_marks_the_run_cancelled_and_it_can_be_resumed(client):
    owner = make_user("owner")
    d1 = register_document(owner)
    wf = _create(client, owner, steps=STEPS[:2])
    entered = asyncio.Event()
    release = asyncio.Event()

    async def blocking(step, ctx):
        entered.set()
        await release.wait()
        return StepOutput(summary="never")

    with patch.dict(
        service_module.ADAPTERS,
        {"extract_fields": _fake_adapter("extract"), "analyze_contract": blocking},
    ):
        started = _run(client, owner, wf["id"], [d1])
        for _ in range(100):
            if entered.is_set():
                break
            time.sleep(0.02)
        assert entered.is_set(), "the run never reached the blocking step"
        # A run in progress cannot be resumed.
        run_url = f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}"
        assert client.post(f"{run_url}/resume", headers=headers(owner)).status_code == 400
        cancelled = client.delete(f"/api/v1/jobs/{started['job_id']}", headers=headers(owner))
        assert cancelled.status_code == 200, cancelled.text
        job = _poll(client, owner, started["job_id"])
        assert job["status"] == "cancelled"
        for _ in range(100):
            run = client.get(run_url, headers=headers(owner)).json()
            if run["status"] == "cancelled":
                break
            time.sleep(0.02)
    assert run["status"] == "cancelled" and run["error"] == "Cancelled before completion"
    assert [s["status"] for s in run["step_runs"]] == ["completed", "failed"]
    assert run["step_runs"][1]["error"] == "Cancelled"

    with patch.dict(
        service_module.ADAPTERS,
        {"extract_fields": _fake_adapter("extract"), "analyze_contract": _fake_adapter("review")},
    ):
        resumed = client.post(f"{run_url}/resume", headers=headers(owner))
        assert resumed.status_code == 202, resumed.text
        assert _poll(client, owner, resumed.json()["job_id"])["status"] == "completed"
    run = client.get(run_url, headers=headers(owner)).json()
    assert run["status"] == "completed" and [s["status"] for s in run["step_runs"]] == [
        "completed",
        "completed",
    ]


def test_editing_the_workflow_does_not_change_a_past_run(client):
    owner = make_user("owner")
    d1 = register_document(owner)
    wf = _create(client, owner, steps=STEPS[:1])
    with patch.dict(service_module.ADAPTERS, {"extract_fields": _fake_adapter("extract")}):
        started = _run(client, owner, wf["id"], [d1])
        assert _poll(client, owner, started["job_id"])["status"] == "completed"
    client.patch(
        f"/api/v1/workflows/{wf['id']}",
        headers=headers(owner),
        json={"steps": [{"name": "review", "kind": "analyze_contract"}]},
    )
    run = client.get(
        f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}", headers=headers(owner)
    ).json()
    assert [s["kind"] for s in run["step_definitions"]] == ["extract_fields"]
    assert [s["name"] for s in run["step_runs"]] == ["extract"]


# ------------------------------------------------------------- a real step


def _model_response(payload: dict) -> MagicMock:
    response = MagicMock()
    response.choices = [MagicMock()]
    response.choices[0].message.content = json.dumps(payload)
    return response


def test_extract_fields_step_creates_a_review_table_the_runner_owns(client):
    owner = make_user("owner")
    d1 = register_document(owner, filename="nda.pdf")
    text = "GOVERNING LAW. This Agreement shall be governed by the laws of the State of Delaware."
    answer = _model_response(
        {
            "fields": {
                "governing_law": {
                    "value": "Delaware",
                    "quote": "laws of the State of Delaware",
                    "not_found": False,
                }
            }
        }
    )
    wf = _create(client, owner, steps=STEPS[:1])
    with (
        patch.object(ReviewTableService, "_llm_client", return_value=MagicMock()),
        patch.object(review_table_service, "document_text", AsyncMock(return_value=text)),
        patch("app.services.review_tables.extraction.openai_chat", AsyncMock(return_value=answer)),
    ):
        started = _run(client, owner, wf["id"], [d1])
        job = _poll(client, owner, started["job_id"])
    assert job["status"] == "completed", job
    run = client.get(
        f"/api/v1/workflows/{wf['id']}/runs/{started['run_id']}", headers=headers(owner)
    ).json()
    step = run["step_runs"][0]
    assert step["status"] == "completed"
    assert (
        step["output"]["summary"]
        == "Extracted 1 field from 1 document: 1 verified, 0 unverified, 0 not found, 0 failed."
    )
    assert step["output"]["data"]["values"] == {d1: {"governing_law": "Delaware"}}
    assert step["output"]["data"]["rows"][d1]["governing_law"]["verified"] is True
    table_id = step["produced_ids"]["review_tables"][0]
    table = client.get(f"/api/v1/review-tables/{table_id}", headers=headers(owner))
    assert table.status_code == 200, table.text
    assert (
        table.json()["name"] == "Intake — extract"
        and table.json()["cells"][0]["value"] == "Delaware"
    )
    assert table.json()["description"] == f"Created by workflow run {started['run_id']}"
