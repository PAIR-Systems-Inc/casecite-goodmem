"""
Device-code sign-in (RFC 8628 style) for clients without a cookie session.

The Word add-in's task pane runs in a sandboxed browser that cannot share the
web app's httpOnly cookies, so it signs in the way a TV app does: it asks for
a code, shows the short user code, and polls until someone signed in to the
web app approves that code on the ``/device`` page. The approval binds the
pending code to the approver's account; the next poll issues a session.

State lives in Redis (so it survives a restart) with the same in-memory
fallback the other short-lived auth stores use. Storage is keyed by a hash of
the device code, never the code itself, so a copy of Redis does not hand out
sessions. The user code is looked up through a second key, also hashed.
"""

from __future__ import annotations

import hashlib
import json
import logging
import secrets
import time
from dataclasses import dataclass
from typing import Literal

from app.config import settings
from app.redis_utils import RedisError, get_redis

logger = logging.getLogger(__name__)

# Letters and digits that are hard to confuse when read off a screen and typed
# into another one: no 0/O, 1/I/L, 8/B, 5/S, 2/Z.
_USER_CODE_ALPHABET = "ACDEFGHJKMNPQRTUVWXY34679"
_USER_CODE_LENGTH = 8

PollStatus = Literal["authorization_pending", "slow_down", "expired_token", "access_denied"]


@dataclass(frozen=True)
class DeviceCodeGrant:
    """What ``start`` hands the client."""

    device_code: str
    user_code: str
    expires_in: int
    interval: int


@dataclass(frozen=True)
class DevicePollResult:
    """Outcome of one poll: a protocol status, or the approving user's id."""

    status: PollStatus | Literal["approved"]
    user_id: str | None = None


def normalize_user_code(raw: str) -> str:
    """``abcd-efgh`` / ``ABCD EFGH`` / ``abcdefgh`` -> ``ABCDEFGH``."""
    return "".join(ch for ch in raw.upper() if ch.isalnum())


def format_user_code(code: str) -> str:
    """``ABCDEFGH`` -> ``ABCD-EFGH`` for display."""
    return f"{code[:4]}-{code[4:]}" if len(code) == _USER_CODE_LENGTH else code


def _hash(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


class DeviceAuthService:
    """Pending device codes: start, approve or deny on the web, poll from the device."""

    def __init__(self) -> None:
        # In-memory fallback: {device_hash: state}, {user_code: device_hash}.
        self._states: dict[str, dict] = {}
        self._by_user_code: dict[str, str] = {}

    # -- storage -----------------------------------------------------------

    @staticmethod
    def _device_key(device_hash: str) -> str:
        return f"device_auth:code:{device_hash}"

    @staticmethod
    def _user_code_key(user_code: str) -> str:
        return f"device_auth:user:{_hash(user_code)}"

    def _ttl_left(self, state: dict) -> int:
        return max(1, int(state["expires_at"] - time.time()))

    def _save(self, device_hash: str, state: dict) -> None:
        ttl = self._ttl_left(state)
        redis_client = get_redis()
        if redis_client:
            try:
                redis_client.setex(self._device_key(device_hash), ttl, json.dumps(state))
                redis_client.setex(self._user_code_key(state["user_code"]), ttl, device_hash)
                return
            except (RedisError, ConnectionError, TimeoutError, OSError, RuntimeError) as e:
                logger.warning("Redis store failed for a device code: %s", e)
        self._states[device_hash] = state
        self._by_user_code[state["user_code"]] = device_hash

    def _load(self, device_hash: str) -> dict | None:
        redis_client = get_redis()
        if redis_client:
            try:
                raw = redis_client.get(self._device_key(device_hash))
                if raw:
                    data = json.loads(raw)
                    return data if isinstance(data, dict) else None
            except (RedisError, ConnectionError, TimeoutError, OSError, RuntimeError) as e:
                logger.warning("Redis read failed for a device code: %s", e)
        self._expire_memory()
        state = self._states.get(device_hash)
        if state and state["expires_at"] <= time.time():
            self._delete(device_hash, state)
            return None
        return state

    def _device_hash_for_user_code(self, user_code: str) -> str | None:
        redis_client = get_redis()
        if redis_client:
            try:
                found = redis_client.get(self._user_code_key(user_code))
                if found:
                    return str(found)
            except (RedisError, ConnectionError, TimeoutError, OSError, RuntimeError) as e:
                logger.warning("Redis read failed for a device user code: %s", e)
        self._expire_memory()
        return self._by_user_code.get(user_code)

    def _delete(self, device_hash: str, state: dict | None) -> None:
        redis_client = get_redis()
        if redis_client:
            try:
                redis_client.delete(self._device_key(device_hash))
                if state:
                    redis_client.delete(self._user_code_key(state["user_code"]))
            except (RedisError, ConnectionError, TimeoutError, OSError, RuntimeError) as e:
                logger.warning("Redis delete failed for a device code: %s", e)
        self._states.pop(device_hash, None)
        if state:
            self._by_user_code.pop(state["user_code"], None)

    def _expire_memory(self) -> None:
        now = time.time()
        for device_hash, state in list(self._states.items()):
            if state["expires_at"] <= now:
                self._states.pop(device_hash, None)
                self._by_user_code.pop(state["user_code"], None)

    # -- protocol ----------------------------------------------------------

    def start(self) -> DeviceCodeGrant:
        """Mint a pending code pair. The device keeps ``device_code`` secret."""
        device_code = secrets.token_urlsafe(32)
        device_hash = _hash(device_code)
        # Retry on the (astronomically unlikely) user-code collision so two
        # pending devices can never share a code.
        for _ in range(5):
            user_code = "".join(
                secrets.choice(_USER_CODE_ALPHABET) for _ in range(_USER_CODE_LENGTH)
            )
            if self._device_hash_for_user_code(user_code) is None:
                break
        now = time.time()
        state = {
            "user_code": user_code,
            "status": "pending",
            "user_id": None,
            "created_at": now,
            "expires_at": now + settings.device_code_ttl,
            "last_poll": 0.0,
        }
        self._save(device_hash, state)
        return DeviceCodeGrant(
            device_code=device_code,
            user_code=format_user_code(user_code),
            expires_in=settings.device_code_ttl,
            interval=settings.device_code_interval,
        )

    def _pending_state_for_user_code(self, raw_user_code: str) -> tuple[str, dict] | None:
        user_code = normalize_user_code(raw_user_code)
        if len(user_code) != _USER_CODE_LENGTH:
            return None
        device_hash = self._device_hash_for_user_code(user_code)
        if device_hash is None:
            return None
        state = self._load(device_hash)
        if state is None:
            return None
        return device_hash, state

    def lookup(self, raw_user_code: str) -> str | None:
        """The status of a user code ("pending"/"approved"/"denied"), or None if unknown."""
        found = self._pending_state_for_user_code(raw_user_code)
        return None if found is None else str(found[1]["status"])

    def approve(self, raw_user_code: str, user_id: str) -> bool:
        """Bind a pending user code to ``user_id``. False when unknown or expired.

        Raises ``ValueError`` when the code has already been decided, so a
        second approval cannot swap the account a device is about to receive.
        """
        found = self._pending_state_for_user_code(raw_user_code)
        if found is None:
            return False
        device_hash, state = found
        if state["status"] != "pending":
            raise ValueError("This code has already been used.")
        state["status"] = "approved"
        state["user_id"] = user_id
        self._save(device_hash, state)
        return True

    def deny(self, raw_user_code: str) -> bool:
        """Refuse a pending user code. False when unknown or expired."""
        found = self._pending_state_for_user_code(raw_user_code)
        if found is None:
            return False
        device_hash, state = found
        if state["status"] != "pending":
            raise ValueError("This code has already been used.")
        state["status"] = "denied"
        self._save(device_hash, state)
        return True

    def poll(self, device_code: str) -> DevicePollResult:
        """One poll from the device. An approved code is consumed on the way out."""
        device_hash = _hash(device_code)
        state = self._load(device_hash)
        if state is None:
            return DevicePollResult("expired_token")
        now = time.time()
        if state["expires_at"] <= now:
            self._delete(device_hash, state)
            return DevicePollResult("expired_token")
        if state["status"] == "denied":
            self._delete(device_hash, state)
            return DevicePollResult("access_denied")
        if now - float(state.get("last_poll") or 0.0) < settings.device_code_interval:
            # Polling faster than the advertised interval; the client must back
            # off, and the pending code stays as it was.
            state["last_poll"] = now
            self._save(device_hash, state)
            return DevicePollResult("slow_down")
        state["last_poll"] = now
        if state["status"] == "approved" and state.get("user_id"):
            self._delete(device_hash, state)
            return DevicePollResult("approved", user_id=str(state["user_id"]))
        self._save(device_hash, state)
        return DevicePollResult("authorization_pending")


device_auth_service = DeviceAuthService()
