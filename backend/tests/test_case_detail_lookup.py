"""Regression tests for case-detail lookup.

The bug: search_cases returns the CLUSTER id (V4), but get_case_detail fetched
/opinions/{id}/ — treating that cluster id as an opinion id, so CourtListener
404'd every case-detail open. get_case_detail is now cluster-first with an
opinion-id fallback.
"""

import os

os.environ.setdefault("SECRET_KEY", "test-secret-key-for-testing-only-32chars!")
os.environ.setdefault("ENCRYPTION_SALT", "test-salt-16chars!")
os.environ.setdefault("DEBUG", "true")

from contextlib import asynccontextmanager
from unittest.mock import patch

from app.services.legal_tools.service import LegalToolsService


class _Resp:
    def __init__(self, status_code: int, data: dict):
        self.status_code = status_code
        self._data = data

    def json(self):
        return self._data

    def raise_for_status(self):
        if self.status_code >= 400:
            raise AssertionError(f"unexpected raise_for_status on {self.status_code}")


class _Client:
    def __init__(self, routes: dict):
        self.routes = routes

    async def get(self, url: str, headers=None):
        for key, resp in self.routes.items():
            if key in url:
                return resp
        return _Resp(404, {})


def _patch_client(routes: dict):
    @asynccontextmanager
    async def _cm(timeout=None):
        yield _Client(routes)

    return patch("app.services.legal_tools._case_lookup.cl_client", _cm)


async def test_get_case_detail_resolves_cluster_id():
    svc = LegalToolsService()
    svc.api_token = "test-token"
    routes = {
        "/clusters/10590063/": _Resp(
            200,
            {
                "case_name": "Foo v. Bar",
                "sub_opinions": ["https://www.courtlistener.com/api/rest/v4/opinions/999/"],
                "citations": [],
                "court": "",
                "date_filed": "2020-01-01",
                "citation_count": 3,
                "precedential_status": "Published",
            },
        ),
        "/opinions/999/": _Resp(200, {"plain_text": "The opinion text here."}),
    }
    with _patch_client(routes):
        result = await svc.get_case_detail(10590063)
    assert result is not None
    assert result["id"] == 10590063
    assert result["case_name"] == "Foo v. Bar"
    assert "opinion text" in result["opinion_text"]


async def test_get_case_detail_falls_back_to_opinion_id():
    svc = LegalToolsService()
    svc.api_token = "test-token"
    routes = {
        "/clusters/55/": _Resp(404, {}),
        "/opinions/55/": _Resp(
            200,
            {
                "plain_text": "Op text",
                "cluster": "https://www.courtlistener.com/api/rest/v4/clusters/77/",
            },
        ),
        "/clusters/77/": _Resp(200, {"case_name": "Baz v. Qux"}),
    }
    with _patch_client(routes):
        result = await svc.get_case_detail(55)
    assert result is not None
    assert result["case_name"] == "Baz v. Qux"
    assert result["id"] == 55


async def test_get_case_detail_returns_none_when_truly_absent():
    svc = LegalToolsService()
    svc.api_token = "test-token"
    routes = {"/clusters/1/": _Resp(404, {}), "/opinions/1/": _Resp(404, {})}
    with _patch_client(routes):
        result = await svc.get_case_detail(1)
    assert result is None


# ---------------------------------------------------------------------------
# Explicit id kind: an opinion id that is also some cluster's id must open the
# opinion's own case, not the same-numbered cluster.
# ---------------------------------------------------------------------------

_AMBIGUOUS_ROUTES = {
    "/clusters/55/": _Resp(200, {"id": 55, "case_name": "Wrong Cluster v. Same Number"}),
    "/opinions/55/": _Resp(
        200,
        {
            "id": 55,
            "plain_text": "Right opinion text",
            "cluster": "https://www.courtlistener.com/api/rest/v4/clusters/77/",
        },
    ),
    "/clusters/77/": _Resp(200, {"id": 77, "case_name": "Right v. Case"}),
}


async def test_opinion_kind_reads_the_opinion_first():
    svc = LegalToolsService()
    svc.api_token = "test-token"
    with _patch_client(_AMBIGUOUS_ROUTES):
        result = await svc.get_case_detail(55, id_kind="opinion")
    assert result["case_name"] == "Right v. Case"
    assert result["resolved_as"] == "opinion"
    assert result["opinion_id"] == 55
    assert result["cluster_id"] == 77
    assert "Right opinion text" in result["opinion_text"]


async def test_cluster_kind_and_bare_id_keep_the_cluster_first_order():
    svc = LegalToolsService()
    svc.api_token = "test-token"
    with _patch_client(_AMBIGUOUS_ROUTES):
        explicit = await svc.get_case_detail(55, id_kind="cluster")
        bare = await svc.get_case_detail(55)
    assert explicit["case_name"] == "Wrong Cluster v. Same Number"
    assert explicit["resolved_as"] == "cluster"
    assert bare["case_name"] == "Wrong Cluster v. Same Number"  # historical behaviour


async def test_opinion_kind_falls_back_to_the_cluster_when_no_such_opinion():
    svc = LegalToolsService()
    svc.api_token = "test-token"
    routes = {
        "/opinions/9/": _Resp(404, {}),
        "/clusters/9/": _Resp(200, {"id": 9, "case_name": "Only a Cluster"}),
    }
    with _patch_client(routes):
        result = await svc.get_case_detail(9, id_kind="opinion")
    assert result["case_name"] == "Only a Cluster"
    assert result["resolved_as"] == "cluster"


async def test_unknown_kind_is_rejected():
    import pytest

    svc = LegalToolsService()
    svc.api_token = "test-token"
    with pytest.raises(ValueError):
        await svc.get_case_detail(1, id_kind="docket")
