"""
Device-code sign-in (RFC 8628 style) for the Word add-in.

Covers the whole flow at the HTTP level: start -> pending -> approve on the
web -> the device's poll issues a labelled session -> the code is consumed;
plus expiry, denial, slow_down, unknown and malformed codes, double decisions,
the MFA-enrolment gate on approval, a deactivated account, and the body-borne
refresh token that only a device session may use.
"""

from __future__ import annotations

import time
from unittest.mock import patch

import pytest
from app.config import settings
from app.middleware.security import session_manager
from app.models.db_models import User as DBUser
from app.services import device_auth
from app.services.device_auth import (
    DeviceAuthService,
    device_auth_service,
    format_user_code,
    normalize_user_code,
)
from sqlalchemy import update
from sqlalchemy.orm import Session

from tests.helpers import _sync_engine, headers, insert_db_user, make_user

pytestmark = pytest.mark.usefixtures("no_rate_limit")

START = "/api/v1/auth/device/start"
APPROVE = "/api/v1/auth/device/approve"
DENY = "/api/v1/auth/device/deny"
TOKEN = "/api/v1/auth/device/token"  # nosec B105 - a URL path, not a secret
REFRESH = "/api/v1/auth/refresh"


def _device_settings(**update):
    """The device-auth module's settings with ``update`` applied (Settings is frozen)."""
    return patch("app.services.device_auth.settings", settings.model_copy(update=update))


@pytest.fixture
def fast_polling():
    """No minimum poll interval, so a test can poll twice in a row."""
    with _device_settings(device_code_interval=0):
        yield


@pytest.fixture
def approver():
    user = make_user("approver")
    insert_db_user(user)
    yield user
    session_manager.terminate_all_sessions(user.id)


def _start(client) -> dict:
    resp = client.post(START)
    assert resp.status_code == 200, resp.text
    return resp.json()


def _poll(client, device_code: str) -> dict:
    resp = client.post(TOKEN, json={"device_code": device_code})
    assert resp.status_code == 200, resp.text
    return resp.json()


class TestCodes:
    def test_normalize_accepts_display_forms(self):
        assert normalize_user_code("abcd-efgh") == "ABCDEFGH"
        assert normalize_user_code(" ABCD EFGH ") == "ABCDEFGH"
        assert format_user_code("ABCDEFGH") == "ABCD-EFGH"

    def test_start_returns_grouped_unambiguous_user_code(self, client):
        grant = _start(client)
        assert grant["expires_in"] == settings.device_code_ttl
        assert grant["interval"] == settings.device_code_interval
        assert len(grant["device_code"]) >= 32
        code = grant["user_code"]
        assert len(code) == 9 and code[4] == "-"
        for ch in code.replace("-", ""):
            assert ch in device_auth._USER_CODE_ALPHABET
        assert grant["verification_uri"].endswith("/device")
        assert grant["verification_uri_complete"].endswith(f"/device?user_code={code}")

    def test_two_starts_never_share_a_user_code(self, client):
        assert _start(client)["user_code"] != _start(client)["user_code"]


class TestFlow:
    def test_pending_until_approved_then_single_use(self, client, approver, fast_polling):
        grant = _start(client)
        assert _poll(client, grant["device_code"])["status"] == "authorization_pending"

        resp = client.post(
            APPROVE, json={"user_code": grant["user_code"]}, headers=headers(approver)
        )
        assert resp.status_code == 200, resp.text
        assert resp.json() == {"status": "approved", "user_code": grant["user_code"]}

        issued = _poll(client, grant["device_code"])
        assert issued["status"] == "approved"
        assert issued["access_token"] and issued["refresh_token"]
        assert issued["user"]["id"] == approver.id
        assert issued["expires_in"] > 0

        # Consumed: the same device code cannot mint a second session.
        assert _poll(client, grant["device_code"])["status"] == "expired_token"

        # The session works as a bearer token and is labelled in the list.
        bearer = {"Authorization": f"Bearer {issued['access_token']}"}
        me = client.get("/api/v1/auth/me", headers=bearer)
        assert me.status_code == 200 and me.json()["id"] == approver.id
        sessions = client.get("/api/v1/auth/sessions", headers=bearer).json()["sessions"]
        assert "Word add-in" in {s["client"] for s in sessions}

    def test_approve_accepts_lowercase_without_hyphen(self, client, approver, fast_polling):
        grant = _start(client)
        loose = grant["user_code"].replace("-", "").lower()
        resp = client.post(APPROVE, json={"user_code": loose}, headers=headers(approver))
        assert resp.status_code == 200
        assert resp.json()["user_code"] == grant["user_code"]
        assert _poll(client, grant["device_code"])["status"] == "approved"

    def test_denied_code_reports_access_denied_once(self, client, approver, fast_polling):
        grant = _start(client)
        resp = client.post(DENY, json={"user_code": grant["user_code"]}, headers=headers(approver))
        assert resp.status_code == 200 and resp.json()["status"] == "denied"
        assert _poll(client, grant["device_code"])["status"] == "access_denied"
        assert _poll(client, grant["device_code"])["status"] == "expired_token"

    def test_polling_faster_than_interval_is_slowed_down(self, client):
        with _device_settings(device_code_interval=60):
            grant = _start(client)
            assert _poll(client, grant["device_code"])["status"] == "authorization_pending"
            assert _poll(client, grant["device_code"])["status"] == "slow_down"

    def test_expired_code_cannot_be_polled_or_approved(self, client, approver, fast_polling):
        grant = _start(client)
        hdrs = headers(approver)
        with patch("app.services.device_auth.time.time", return_value=time.time() + 10_000):
            assert _poll(client, grant["device_code"])["status"] == "expired_token"
            resp = client.post(APPROVE, json={"user_code": grant["user_code"]}, headers=hdrs)
            assert resp.status_code == 404

    def test_unknown_device_code_is_expired_token(self, client):
        assert _poll(client, "x" * 43)["status"] == "expired_token"

    def test_unknown_user_code_is_404_and_malformed_is_422(self, client, approver):
        resp = client.post(APPROVE, json={"user_code": "ZZZZ-ZZZZ"}, headers=headers(approver))
        assert resp.status_code == 404
        resp = client.post(APPROVE, json={"user_code": "ABC"}, headers=headers(approver))
        assert resp.status_code == 422

    def test_second_decision_is_a_conflict(self, client, approver):
        grant = _start(client)
        other = make_user("other")
        insert_db_user(other)
        try:
            first = client.post(
                APPROVE, json={"user_code": grant["user_code"]}, headers=headers(approver)
            )
            assert first.status_code == 200
            again = client.post(
                APPROVE, json={"user_code": grant["user_code"]}, headers=headers(other)
            )
            assert again.status_code == 409
            denied = client.post(
                DENY, json={"user_code": grant["user_code"]}, headers=headers(other)
            )
            assert denied.status_code == 409
        finally:
            session_manager.terminate_all_sessions(other.id)

    def test_approve_and_deny_require_sign_in(self, client):
        grant = _start(client)
        assert client.post(APPROVE, json={"user_code": grant["user_code"]}).status_code == 401
        assert client.post(DENY, json={"user_code": grant["user_code"]}).status_code == 401

    def test_mfa_required_account_cannot_approve_before_enrolling(self, client, approver):
        grant = _start(client)
        forced = settings.model_copy(update={"require_mfa": True})
        with patch("app.services.auth.settings", forced):
            resp = client.post(
                APPROVE, json={"user_code": grant["user_code"]}, headers=headers(approver)
            )
        assert resp.status_code == 403
        assert resp.json().get("code") == "mfa_enrollment_required"
        # Nothing was bound: the device keeps waiting.
        assert _poll(client, grant["device_code"])["status"] == "authorization_pending"

    def test_deactivated_account_gets_no_session(self, client, approver, fast_polling):
        grant = _start(client)
        resp = client.post(
            APPROVE, json={"user_code": grant["user_code"]}, headers=headers(approver)
        )
        assert resp.status_code == 200
        engine = _sync_engine()
        try:
            with Session(engine) as session:
                session.execute(
                    update(DBUser).where(DBUser.id == approver.id).values(is_active=False)
                )
                session.commit()
        finally:
            engine.dispose()
        resp = client.post(TOKEN, json={"device_code": grant["device_code"]})
        assert resp.status_code == 403


class TestDeviceRefresh:
    def _device_tokens(self, client, approver) -> dict:
        grant = _start(client)
        resp = client.post(
            APPROVE, json={"user_code": grant["user_code"]}, headers=headers(approver)
        )
        assert resp.status_code == 200
        issued = _poll(client, grant["device_code"])
        assert issued["status"] == "approved"
        return issued

    def test_body_refresh_token_rotates_for_a_device_session(self, client, approver, fast_polling):
        issued = self._device_tokens(client, approver)
        client.cookies.clear()
        resp = client.post(REFRESH, json={"refresh_token": issued["refresh_token"]})
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert body["access_token"] != issued["access_token"]
        assert body["refresh_token"] and body["refresh_token"] != issued["refresh_token"]
        # No cookie was set for a cookie-less client.
        assert "refresh_token" not in resp.cookies
        # The rotated token works; the spent one does not.
        again = client.post(REFRESH, json={"refresh_token": body["refresh_token"]})
        assert again.status_code == 200
        replay = client.post(REFRESH, json={"refresh_token": issued["refresh_token"]})
        assert replay.status_code == 401

    def test_body_refresh_token_is_refused_for_a_cookie_session(self, client, approver):
        from app.services.auth import auth_service

        cookie_style = auth_service.create_refresh_token(approver, session_id="sid-1")
        client.cookies.clear()
        resp = client.post(REFRESH, json={"refresh_token": cookie_style})
        assert resp.status_code == 401

    def test_refresh_without_any_token_is_401(self, client):
        client.cookies.clear()
        assert client.post(REFRESH, json={}).status_code == 401
        assert client.post(REFRESH, content=b"not json").status_code == 401


class TestServiceFallback:
    def test_in_memory_store_round_trip(self):
        svc = DeviceAuthService()
        with (
            _device_settings(device_code_interval=0),
            patch("app.services.device_auth.get_redis", return_value=None),
        ):
            grant = svc.start()
            assert svc.lookup(grant.user_code) == "pending"
            assert svc.poll(grant.device_code).status == "authorization_pending"
            assert svc.approve(grant.user_code, "u-1") is True
            result = svc.poll(grant.device_code)
            assert result.status == "approved" and result.user_id == "u-1"
            assert svc.lookup(grant.user_code) is None
            assert svc.approve(grant.user_code, "u-2") is False

    def test_memory_entries_expire(self):
        svc = DeviceAuthService()
        with (
            _device_settings(device_code_ttl=1),
            patch("app.services.device_auth.get_redis", return_value=None),
        ):
            grant = svc.start()
            with patch("app.services.device_auth.time.time", return_value=time.time() + 5):
                assert svc.lookup(grant.user_code) is None
                assert svc.poll(grant.device_code).status == "expired_token"
                assert svc._states == {} and svc._by_user_code == {}

    def test_module_singleton_exists(self):
        assert isinstance(device_auth_service, DeviceAuthService)
