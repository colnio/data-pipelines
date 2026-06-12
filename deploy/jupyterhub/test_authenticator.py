"""
test_authenticator.py — pytest suite for LabDataAuthenticator.

No network, no JupyterHub daemon required.  _login is monkeypatched to return
controlled fixtures.  The test file imports labdata_authenticator directly from
the deploy/jupyterhub directory.

Run from repo root:
    deploy/jupyterhub/.venv/bin/python -m pytest deploy/jupyterhub/test_authenticator.py -q
"""
from __future__ import annotations

import re
import sys
import os

# Make deploy/jupyterhub importable (labdata_authenticator lives there).
_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

import pytest

# Import the module under test — this must succeed even without jupyterhub
# because labdata_authenticator.py has an ImportError guard.
from labdata_authenticator import LabDataAuthenticator

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

VALID_UNIX_RE = re.compile(r"^[a-z][a-z0-9_-]{0,30}$")


def _make_auth() -> LabDataAuthenticator:
    """Instantiate the authenticator without a running Hub."""
    return LabDataAuthenticator()


# ---------------------------------------------------------------------------
# Async test support: run coroutines with asyncio.run()
# ---------------------------------------------------------------------------
import asyncio


def _run(coro):
    return asyncio.run(coro)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestNormalizeUsername:
    def test_basic_email(self):
        assert LabDataAuthenticator.normalize_username("alice@nus.edu.sg") == "alice"

    def test_mixed_case(self):
        assert LabDataAuthenticator.normalize_username("Jane.Doe@nus.edu.sg") == "jane_doe"

    def test_plus_address(self):
        assert LabDataAuthenticator.normalize_username("user+tag@example.com") == "user_tag"

    def test_valid_unix_handle(self):
        result = LabDataAuthenticator.normalize_username("Jane.Doe@nus.edu.sg")
        # Must start with a letter, contain only [a-z0-9_-], length <= 32
        assert re.match(r"^[a-z][a-z0-9_-]*$", result), (
            f"normalize_username returned '{result}' which is not a valid Unix handle"
        )

    def test_no_at_sign(self):
        # Graceful degradation: treat the whole string as the local part.
        result = LabDataAuthenticator.normalize_username("justlocal")
        assert re.match(r"^[a-z][a-z0-9_-]*$", result)

    def test_special_chars_replaced(self):
        result = LabDataAuthenticator.normalize_username("first.last+sub@lab.local")
        assert "." not in result
        assert "+" not in result
        assert "@" not in result


class TestAuthenticate:
    """
    Test LabDataAuthenticator.authenticate() with monkeypatched _login.
    """

    def _auth_with_login(self, login_return_value, email="user@nus.edu.sg", password="pw"):
        """Run authenticate() with _login mocked to return login_return_value."""
        auth = _make_auth()

        async def fake_login(self_inner, e, p):
            return login_return_value

        auth._login = fake_login.__get__(auth, type(auth))  # bind as method
        handler = None  # authenticate() never touches handler in our impl
        data = {"username": email, "password": password}
        return _run(auth.authenticate(handler, data))

    # -- Happy path ----------------------------------------------------------

    def test_active_user_returns_dict(self):
        result = self._auth_with_login({"global_role": "member", "email": "user@nus.edu.sg"})
        assert isinstance(result, dict)
        assert "name" in result
        assert "admin" in result

    def test_active_user_name_is_normalized(self):
        result = self._auth_with_login(
            {"global_role": "member"}, email="Jane.Doe@nus.edu.sg"
        )
        assert result is not None
        assert result["name"] == "jane_doe"

    # -- Admin / role mapping ------------------------------------------------

    def test_admin_role_is_admin(self):
        result = self._auth_with_login({"global_role": "admin"})
        assert result is not None
        assert result["admin"] is True

    def test_pi_role_is_admin(self):
        result = self._auth_with_login({"global_role": "pi"})
        assert result is not None
        assert result["admin"] is True

    def test_member_role_is_not_admin(self):
        result = self._auth_with_login({"global_role": "member"})
        assert result is not None
        assert result["admin"] is False

    def test_unknown_role_is_not_admin(self):
        result = self._auth_with_login({"global_role": "viewer"})
        assert result is not None
        assert result["admin"] is False

    # -- Failure paths -------------------------------------------------------

    def test_failed_login_returns_none(self):
        """_login returning None must cause authenticate to return None."""
        result = self._auth_with_login(None)
        assert result is None

    def test_empty_password_returns_none(self):
        auth = _make_auth()

        async def should_not_be_called(self_inner, e, p):
            raise AssertionError("_login must not be called with empty password")

        auth._login = should_not_be_called.__get__(auth, type(auth))
        data = {"username": "user@nus.edu.sg", "password": ""}
        result = _run(auth.authenticate(None, data))
        assert result is None

    def test_empty_username_returns_none(self):
        auth = _make_auth()

        async def should_not_be_called(self_inner, e, p):
            raise AssertionError("_login must not be called with empty username")

        auth._login = should_not_be_called.__get__(auth, type(auth))
        data = {"username": "", "password": "hunter2"}
        result = _run(auth.authenticate(None, data))
        assert result is None

    # -- Returned dict shape -------------------------------------------------

    def test_returned_dict_has_exactly_name_and_admin(self):
        result = self._auth_with_login({"global_role": "member"})
        assert result is not None
        assert set(result.keys()) == {"name", "admin"}


# ---------------------------------------------------------------------------
# Real _login HTTP path: faking only AsyncHTTPClient.fetch so the real
# HTTPRequest construction + fetch(raise_error=...) wiring is exercised.
# (A mock that patches _login entirely would NOT catch a bad HTTPRequest kwarg.)
# ---------------------------------------------------------------------------
import asyncio as _asyncio
import json as _json
import labdata_authenticator as _la


class _FakeResp:
    def __init__(self, code, body):
        self.code = code
        self.body = body


class _FakeClient:
    def __init__(self, resp):
        self._resp = resp

    async def fetch(self, req, raise_error=False):
        # raise_error must be passed to fetch(), never to HTTPRequest.
        assert raise_error is False
        return self._resp


class TestLoginHTTPPath:
    def _run(self, resp, monkeypatch):
        monkeypatch.setattr(_la, "AsyncHTTPClient", lambda: _FakeClient(resp))
        auth = LabDataAuthenticator()
        return _asyncio.run(auth._login("user@nus.edu.sg", "pw"))

    def test_login_200_returns_parsed_json(self, monkeypatch):
        body = _json.dumps({"user": {"global_role": "member"}}).encode()
        out = self._run(_FakeResp(200, body), monkeypatch)
        assert out == {"user": {"global_role": "member"}}

    def test_login_401_returns_none(self, monkeypatch):
        out = self._run(_FakeResp(401, b'{"code":"unauthorized"}'), monkeypatch)
        assert out is None
