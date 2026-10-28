"""HTTP-level tests for the authority-map router (/api/v1/authority-map)."""

import uuid
from unittest.mock import AsyncMock, patch

import pytest
from app.models.authority_map import AuthorityMapping, AuthorityMapRun
from app.services.audit import AuditEventType

from tests.helpers import (
    db_add,
    db_count,
    headers,
    make_shared_matter,
    make_user,
    register_document,
)

pytestmark = pytest.mark.usefixtures("no_rate_limit")

BASE = "/api/v1/authority-map"


def _seed_run(owner) -> str:
    run_id = uuid.uuid4().hex
    db_add(
        AuthorityMapRun(id=run_id, user_id=owner.id, document_name="brief.docx", summary={"n": 1}),
        AuthorityMapping(
            run_id=run_id,
            proposition="Summary judgment requires no genuine dispute of material fact.",
            source="courtlistener",
            case_name="Celotex Corp. v. Catrett",
            verified=1,
            relevance=0.9,
        ),
    )
    return run_id


class TestAuthAndPermission:
    def test_requires_authentication(self, client):
        assert client.post(f"{BASE}/analyze", json={"document_text": "x"}).status_code == 401
        assert client.get(f"{BASE}/abc").status_code == 401
        assert client.delete(f"{BASE}/abc").status_code == 401

    def test_viewer_lacks_authority_map_permission(self, client, non_admin_headers):
        resp = client.post(
            f"{BASE}/analyze", json={"document_text": "x"}, headers=non_admin_headers
        )
        assert resp.status_code == 403
        assert client.get(f"{BASE}/abc", headers=non_admin_headers).status_code == 403


class TestAnalyze:
    def test_submits_job_for_the_caller(self, client, audit_events):
        owner = make_user("am-owner")
        with patch(
            "app.routers.authority_map.job_manager.submit",
            new_callable=AsyncMock,
            return_value="job-123",
        ) as submit:
            resp = client.post(
                f"{BASE}/analyze",
                json={"document_text": "The court should grant the motion.", "jurisdiction": "ca9"},
                headers=headers(owner),
            )
        assert resp.status_code == 200
        assert resp.json() == {"job_id": "job-123"}
        assert submit.await_args.kwargs["user_id"] == owner.id
        assert [e["details"]["action"] for e in audit_events] == ["authority_map_submit"]

    def test_blank_document_is_rejected(self, client):
        owner = make_user("am-owner")
        resp = client.post(f"{BASE}/analyze", json={"document_text": "   "}, headers=headers(owner))
        assert resp.status_code == 400

    def test_oversized_document_is_rejected(self, client):
        owner = make_user("am-owner")
        resp = client.post(
            f"{BASE}/analyze", json={"document_text": "x" * 2_000_001}, headers=headers(owner)
        )
        assert resp.status_code == 422

    def _analyze_with_document(self, client, user, document_id):
        from app.services.documents import document_service

        with patch(
            "app.routers.authority_map.job_manager.submit",
            new_callable=AsyncMock,
            return_value="job-456",
        ) as submit:
            try:
                resp = client.post(
                    f"{BASE}/analyze",
                    json={"document_text": "The court should grant.", "document_id": document_id},
                    headers=headers(user),
                )
            finally:
                document_service.documents.pop(document_id, None)
        return resp, submit

    def test_unknown_document_id_is_404(self, client):
        """document_id is stored on the run as its source: it must be a
        document the caller can read, not an arbitrary client-supplied id."""
        owner = make_user("am-owner")
        resp, submit = self._analyze_with_document(client, owner, "doc-does-not-exist")
        assert resp.status_code == 404
        submit.assert_not_awaited()

    def test_another_users_document_id_is_404(self, client):
        owner, stranger = make_user("am-owner"), make_user("am-stranger")
        doc_id = register_document(owner)
        resp, submit = self._analyze_with_document(client, stranger, doc_id)
        assert resp.status_code == 404
        submit.assert_not_awaited()

    def test_own_document_id_is_accepted(self, client):
        owner = make_user("am-owner")
        doc_id = register_document(owner)
        resp, submit = self._analyze_with_document(client, owner, doc_id)
        assert resp.status_code == 200, resp.text
        submit.assert_awaited_once()

    def test_document_shared_through_a_matter_is_accepted(self, client):
        owner, member = make_user("am-owner"), make_user("am-member")
        matter_id = make_shared_matter(owner, member)
        doc_id = register_document(owner, matter_id=matter_id)
        resp, _ = self._analyze_with_document(client, member, doc_id)
        assert resp.status_code == 200, resp.text


class TestReadAndDelete:
    def test_owner_reads_run_and_it_is_audited(self, client, audit_events):
        owner = make_user("am-owner")
        run_id = _seed_run(owner)

        resp = client.get(f"{BASE}/{run_id}", headers=headers(owner))
        assert resp.status_code == 200
        body = resp.json()
        assert body["run_id"] == run_id
        assert body["document_name"] == "brief.docx"
        assert body["mappings"][0]["case_name"] == "Celotex Corp. v. Catrett"
        assert body["mappings"][0]["verified"] is True

        assert len(audit_events) == 1
        assert audit_events[0]["event_type"] == AuditEventType.DATA_ACCESS
        assert audit_events[0]["resource_id"] == run_id

    def test_unknown_run_is_404(self, client):
        owner = make_user("am-owner")
        assert client.get(f"{BASE}/nope", headers=headers(owner)).status_code == 404

    def test_owner_deletes_run_and_mappings(self, client, audit_events):
        owner = make_user("am-owner")
        run_id = _seed_run(owner)

        resp = client.delete(f"{BASE}/{run_id}", headers=headers(owner))
        assert resp.status_code == 200
        assert resp.json() == {"status": "deleted", "id": run_id}
        assert db_count(AuthorityMapRun, AuthorityMapRun.id == run_id) == 0
        assert db_count(AuthorityMapping, AuthorityMapping.run_id == run_id) == 0
        assert client.get(f"{BASE}/{run_id}", headers=headers(owner)).status_code == 404

        deletions = [e for e in audit_events if e["event_type"] == AuditEventType.DATA_DELETION]
        assert [e["resource_id"] for e in deletions] == [run_id]
