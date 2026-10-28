"""
Domain exceptions (app.exceptions) must come out of the API exactly as the
equivalent ``HTTPException`` does: same status, same JSON body, same headers,
same audit entry for denials, same hidden detail for 5xx outside DEBUG.
"""

from unittest.mock import AsyncMock, patch

import pytest
from app.config import settings
from app.error_handlers import register_error_handlers
from app.exceptions import (
    AppError,
    BadRequestError,
    ConflictError,
    ForbiddenError,
    NotFoundError,
    RateLimitedError,
    ServiceUnavailableError,
    UnauthorizedError,
    UnprocessableError,
    UpstreamError,
)
from fastapi import FastAPI, HTTPException
from fastapi.testclient import TestClient

EXPECTED_STATUS = {
    BadRequestError: 400,
    UnauthorizedError: 401,
    ForbiddenError: 403,
    NotFoundError: 404,
    ConflictError: 409,
    UnprocessableError: 422,
    RateLimitedError: 429,
    UpstreamError: 502,
    ServiceUnavailableError: 503,
}


def _app() -> FastAPI:
    """A tiny app with the real handlers and one route per way of failing."""
    app = FastAPI()
    register_error_handlers(app)

    @app.get("/app-error/{name}")
    async def raise_app_error(name: str):
        cls = {c.__name__: c for c in EXPECTED_STATUS}[name]
        raise cls(f"{name} detail")

    @app.get("/http-error/{status_code}")
    async def raise_http_error(status_code: int):
        raise HTTPException(status_code=status_code, detail=f"{status_code} detail")

    @app.get("/with-headers")
    async def raise_with_headers():
        raise UnauthorizedError("Sign in", headers={"WWW-Authenticate": "Bearer"})

    @app.get("/base")
    async def raise_base():
        raise AppError("teapot", status_code=418)

    return app


@pytest.fixture
def client():
    with TestClient(_app(), raise_server_exceptions=False) as c:
        yield c


@pytest.fixture
def no_audit():
    """Keep the handlers' 401/403 audit writes out of the real audit log."""
    with patch("app.error_handlers.audit_service.log_event", new_callable=AsyncMock) as log:
        yield log


class TestExceptionClasses:
    @pytest.mark.parametrize(("cls", "status"), EXPECTED_STATUS.items())
    def test_status_detail_and_headers(self, cls, status):
        exc = cls("why")
        assert isinstance(exc, AppError)
        assert isinstance(exc, Exception)
        assert exc.status_code == status
        assert exc.detail == "why"
        assert exc.headers is None
        assert str(exc) == "why"
        assert cls.__name__ in repr(exc)

    def test_headers_and_explicit_status_are_kept(self):
        exc = AppError("x", status_code=418, headers={"Retry-After": "1"})
        assert exc.status_code == 418
        assert exc.headers == {"Retry-After": "1"}


class TestResponsesMatchHTTPException:
    @pytest.mark.parametrize(("cls", "status"), EXPECTED_STATUS.items())
    def test_same_status_and_body_shape_as_http_exception(self, client, no_audit, cls, status):
        ours = client.get(f"/app-error/{cls.__name__}")
        theirs = client.get(f"/http-error/{status}")
        assert ours.status_code == status == theirs.status_code
        assert set(ours.json()) == set(theirs.json()) == {"detail", "error_ref"}
        assert ours.json()["detail"] == f"{cls.__name__} detail"
        if status >= 500:
            # DEBUG is on in tests: the detail is shown and an error_ref is attached.
            assert ours.json()["error_ref"].startswith("ERR-")
        else:
            assert ours.json()["error_ref"] is None
        assert "code" not in ours.json()

    def test_base_class_uses_its_explicit_status(self, client, no_audit):
        resp = client.get("/base")
        assert resp.status_code == 418
        assert resp.json() == {"detail": "teapot", "error_ref": None}

    def test_headers_are_sent(self, client, no_audit):
        resp = client.get("/with-headers")
        assert resp.status_code == 401
        assert resp.headers["WWW-Authenticate"] == "Bearer"
        assert resp.json() == {"detail": "Sign in", "error_ref": None}

    def test_5xx_detail_is_hidden_outside_debug(self, client, no_audit):
        production = settings.model_copy(update={"debug": False})
        with patch("app.error_handlers.settings", production):
            ours = client.get("/app-error/UpstreamError")
            theirs = client.get("/http-error/502")
        assert ours.status_code == theirs.status_code == 502
        assert ours.json()["detail"] == theirs.json()["detail"]
        assert ours.json()["detail"] == "An internal error occurred. Please try again."
        assert ours.json()["error_ref"].startswith("ERR-")

    @pytest.mark.parametrize("name", ["UnauthorizedError", "ForbiddenError"])
    def test_denials_are_audited_like_http_exceptions(self, client, no_audit, name):
        from app import error_handlers

        error_handlers._anon_401_last_audit.clear()
        resp = client.get(f"/app-error/{name}", headers={"Authorization": "Bearer x"})
        assert resp.status_code == EXPECTED_STATUS[{c.__name__: c for c in EXPECTED_STATUS}[name]]
        assert no_audit.await_count == 1
        details = no_audit.await_args.kwargs["details"]
        assert details["status_code"] == resp.status_code
        assert details["detail"] == f"{name} detail"
        assert details["path"] == f"/app-error/{name}"

    def test_404_is_not_audited(self, client, no_audit):
        client.get("/app-error/NotFoundError")
        assert no_audit.await_count == 0
