"""
Integration tests for the Chat router (/chat).
"""

import os

os.environ["SECRET_KEY"] = "test-secret-key-for-testing-only-32chars!"
os.environ["ENCRYPTION_SALT"] = "test-salt-16chars!"
os.environ["DEBUG"] = "true"

from unittest.mock import AsyncMock, patch

import pytest


class TestChatRouter:
    """Tests for /chat endpoints."""

    def test_chat_post_with_auth(self, app, client, auth_headers):
        """POST /chat with auth and mocked RAG service returns 200."""
        mock_result = {
            "content": "This is a test legal response.",
            "citations": [],
            "stats": {
                "docs_searched": 10,
                "chunks_retrieved": 2,
                "processing_time": "0.5s",
                "model": "gpt-4o",
            },
        }

        with patch(
            "app.routers.chat.rag_service.generate_response",
            new_callable=AsyncMock,
            return_value=mock_result,
        ):
            response = client.post(
                "/api/v1/chat",
                json={
                    "query": "What is the statute of limitations for breach of contract?",
                    "mode": "research",
                },
                headers=auth_headers,
            )
            assert response.status_code == 200
            data = response.json()
            assert "content" in data
            assert "id" in data
            assert "citations" in data

    def test_chat_post_no_auth(self, client):
        """POST /chat without auth returns 401 or 403."""
        response = client.post(
            "/api/v1/chat",
            json={"query": "test question", "mode": "research"},
        )
        assert response.status_code in [401, 403]


def _route(intent: str):
    return patch(
        "app.routers.chat.route_query",
        new_callable=AsyncMock,
        return_value=(intent, False, None, False),
    )


class TestStrategyRouting:
    """Strategy briefs scope to every selected folder and keep citation facts."""

    BRIEF = {
        "question": "q",
        "position": "Strong position.",
        "scope": {"documents_considered": 2},
        "strengths": [],
        "weaknesses": [],
        "next_steps": [],
        "citations": [
            {
                "id": "s1",
                "source": "Roe v. Wade",
                "type": "case_law",
                "confidence": 90,
                "passage": "quoted",
                "opinionId": "123",
                "cluster_id": "108713",
                "url": "https://www.courtlistener.com/opinion/108713/roe-v-wade/",
                "verified": True,
                "explanation": "Supports the point.",
            },
            {"id": "s2", "source": "lease.pdf", "type": "document", "confidence": 0.5},
        ],
    }

    def test_every_selected_folder_scopes_the_brief_and_ids_survive(self, client, auth_headers):
        with (
            _route("strategy"),
            patch(
                "app.routers.chat.document_service.get_documents_by_filter",
                new_callable=AsyncMock,
                return_value=["d1", "d2"],
            ),
            patch(
                "app.routers.chat.strategy_service.generate_brief",
                new_callable=AsyncMock,
                return_value=self.BRIEF,
            ) as brief,
        ):
            resp = client.post(
                "/api/v1/chat",
                json={
                    "query": "What is our position on the indemnity dispute?",
                    "mode": "research",
                    "document_filter": {"folder_paths": ["/Cases/Smith/", "/Cases/Jones/"]},
                },
                headers=auth_headers,
            )
        assert resp.status_code == 200, resp.text
        assert brief.await_args.kwargs["folder_paths"] == ["/Cases/Smith/", "/Cases/Jones/"]

        case, doc = resp.json()["citations"]
        assert case["opinion_id"] == "123"
        assert case["cluster_id"] == "108713"
        assert case["verified"] is True
        assert case["url"].endswith("/roe-v-wade/")
        assert doc["opinion_id"] is None and doc["verified"] is None


class TestSharedMatterScope:
    def test_callers_matters_reach_retrieval(self, app, client, auth_headers):
        """Documents shared through a matter are searchable in chat."""
        from app.routers._matter_deps import accessible_matter_ids

        app.dependency_overrides[accessible_matter_ids] = lambda: {"matter-b", "matter-a"}
        try:
            with (
                _route("ask"),
                patch(
                    "app.routers.chat.rag_service.generate_response",
                    new_callable=AsyncMock,
                    return_value={
                        "content": "ok",
                        "citations": [],
                        "stats": {
                            "docs_searched": 1,
                            "chunks_retrieved": 1,
                            "processing_time": "0s",
                        },
                    },
                ) as generate,
            ):
                resp = client.post(
                    "/api/v1/chat",
                    json={"query": "Which leases auto-renew?", "mode": "research"},
                    headers=auth_headers,
                )
        finally:
            app.dependency_overrides.pop(accessible_matter_ids, None)
        assert resp.status_code == 200, resp.text
        assert generate.await_args.kwargs["matter_ids"] == ["matter-a", "matter-b"]


class TestAuthorityMapPersistence:
    """A chat authority map is saved with the session: the running job on the
    message right away, the run ids and totals when the job finishes."""

    def test_record_is_written_with_the_message_and_completed_by_the_job(
        self, client, auth_headers
    ):
        from types import SimpleNamespace

        analysis = {
            "run_id": "run-1",
            "document_name": "brief.docx",
            "jurisdiction": None,
            "summary": {"propositions": 2, "authorities": 1, "verified": 1, "coverage": {}},
            "mappings": [],
        }
        with (
            _route("authority_map"),
            patch(
                "app.routers.chat.document_service.get_documents_by_filter",
                new_callable=AsyncMock,
                return_value=["doc-1"],
            ),
            patch(
                "app.routers.chat.document_service.get_document",
                new_callable=AsyncMock,
                return_value=SimpleNamespace(filename="brief.docx"),
            ),
            patch(
                "app.routers.chat.document_service.get_document_text",
                new_callable=AsyncMock,
                return_value=("brief.docx", "Summary judgment requires no genuine dispute."),
            ),
            patch(
                "app.routers.chat.authority_mapper_service.analyze",
                new_callable=AsyncMock,
                return_value=analysis,
            ),
            patch(
                "app.routers.chat.job_manager.submit", new_callable=AsyncMock, return_value="job-1"
            ) as submit,
        ):
            resp = client.post(
                "/api/v1/chat",
                json={
                    "query": "Give me all the case law supporting this brief",
                    "mode": "research",
                    "document_filter": {"document_ids": ["doc-1"]},
                },
                headers=auth_headers,
            )
            assert resp.status_code == 200, resp.text
            data = resp.json()
            assert data["authority_map_job"] == {
                "job_id": "job-1",
                "documents": [{"id": "doc-1", "name": "brief.docx"}],
            }
            session_id = data["session_id"]
            assert session_id

            thread = client.get(f"/api/v1/chat/sessions/{session_id}", headers=auth_headers)
            assistant = thread.json()["messages"][1]
            assert assistant["authority_map"] == {
                "job_id": "job-1",
                "documents": [{"id": "doc-1", "name": "brief.docx"}],
                "status": "running",
            }

            # Run the submitted job inside the app's event loop, as the job
            # manager would, and check it completes the stored record.
            job = submit.await_args.args[0]
            result = client.portal.call(job)
            assert result["totals"] == {
                "files": 1,
                "propositions": 2,
                "authorities": 1,
                "verified": 1,
            }

            thread = client.get(f"/api/v1/chat/sessions/{session_id}", headers=auth_headers)
            record = thread.json()["messages"][1]["authority_map"]
            assert record["status"] == "completed"
            assert record["job_id"] == "job-1"
            assert record["runs"] == [
                {
                    "document_id": "doc-1",
                    "document_name": "brief.docx",
                    "run_id": "run-1",
                    "summary": analysis["summary"],
                }
            ]
            assert record["totals"]["verified"] == 1
            assert record["coverage_notes"] == []

    def test_failed_job_is_recorded_as_failed(self, client, auth_headers):
        from types import SimpleNamespace

        with (
            _route("authority_map"),
            patch(
                "app.routers.chat.document_service.get_documents_by_filter",
                new_callable=AsyncMock,
                return_value=["doc-1"],
            ),
            patch(
                "app.routers.chat.document_service.get_document",
                new_callable=AsyncMock,
                return_value=SimpleNamespace(filename="brief.docx"),
            ),
            patch(
                "app.routers.chat.document_service.get_document_text",
                new_callable=AsyncMock,
                side_effect=RuntimeError("storage down"),
            ),
            patch(
                "app.routers.chat.job_manager.submit", new_callable=AsyncMock, return_value="job-2"
            ) as submit,
        ):
            resp = client.post(
                "/api/v1/chat",
                json={
                    "query": "Map all the authority for this file",
                    "mode": "research",
                    "document_filter": {"document_ids": ["doc-1"]},
                },
                headers=auth_headers,
            )
            session_id = resp.json()["session_id"]
            job = submit.await_args.args[0]
            with pytest.raises(RuntimeError):
                client.portal.call(job)

            thread = client.get(f"/api/v1/chat/sessions/{session_id}", headers=auth_headers)
            record = thread.json()["messages"][1]["authority_map"]
            assert record["status"] == "failed"
            assert record["error"] == "RuntimeError"
            assert record["job_id"] == "job-2"
