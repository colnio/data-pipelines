"""
labdata_authenticator.py — JupyterHub authenticator for the Lab Data system.

Authenticates users against the Go API (architecture §18 "authenticate users
through the same lab identity mechanism where possible").

Class: LabDataAuthenticator(LocalAuthenticator)
  - POSTs {email, password} to ${LABDATA_API_URL}/v1/auth/login
  - Returns {'name': <normalized>, 'admin': <bool>} on success, None on failure
  - HTTP call is isolated in _login() so tests can monkeypatch without a network

normalize_username(email):
  - lowercase the local part (before @)
  - replace any char not in [a-z0-9_-] with '_'
  - valid Linux/PAM username for TLJH create_system_users=True
"""
from __future__ import annotations

import json
import logging
import os
import re

# Guard: only import JupyterHub pieces when the package is available so that
# this file can be imported in test environments that only have tornado installed
# but not a full JupyterHub.  The test suite monkeypatches _login directly.
try:
    from jupyterhub.auth import LocalAuthenticator as _LocalAuthenticator
except ImportError:  # pragma: no cover
    # Minimal shim so the module loads in test envs without jupyterhub installed.
    class _LocalAuthenticator:  # type: ignore[no-redef]
        """Minimal stand-in so the class hierarchy is preserved in tests."""
        def normalize_username(self, username: str) -> str:  # noqa: D102
            return username

try:
    from tornado.httpclient import AsyncHTTPClient, HTTPRequest
    from tornado.escape import json_decode
except ImportError:  # pragma: no cover
    AsyncHTTPClient = None  # type: ignore[assignment,misc]
    HTTPRequest = None  # type: ignore[assignment,misc]
    json_decode = json.loads  # type: ignore[assignment]

log = logging.getLogger(__name__)

_LABDATA_API_URL_DEFAULT = "http://localhost:8080"
_USERNAME_SAFE = re.compile(r"[^a-z0-9_-]")


class LabDataAuthenticator(_LocalAuthenticator):
    """
    JupyterHub authenticator that delegates credential validation to the
    Lab Data Go API (POST /v1/auth/login).

    Portable between TLJH (production) and the dev container
    (SimpleLocalProcessSpawner) — only the spawner differs.

    Configuration:
        LABDATA_API_URL  env var  default: http://localhost:8080
    """

    # ------------------------------------------------------------------
    # Public overrides
    # ------------------------------------------------------------------

    async def authenticate(self, handler, data: dict) -> dict | None:
        """
        Authenticate a user via the Lab Data API.

        Returns a JupyterHub auth dict::

            {'name': <unix-safe username>, 'admin': <bool>}

        or None if credentials are invalid / the API is unreachable.
        """
        email: str = (data.get("username") or "").strip()
        password: str = data.get("password") or ""
        if not email or not password:
            return None

        user_info = await self._login(email, password)
        if user_info is None:
            return None

        try:
            global_role = user_info.get("global_role", "")
            is_admin = global_role in ("admin", "pi")
            username = self.normalize_username(email)
            return {"name": username, "admin": is_admin}
        except Exception:
            log.exception("LabDataAuthenticator: unexpected error parsing API response")
            return None

    # ------------------------------------------------------------------
    # Overridable HTTP helper (monkeypatched in tests)
    # ------------------------------------------------------------------

    async def _login(self, email: str, password: str) -> dict | None:
        """
        POST {email, password} to ${LABDATA_API_URL}/v1/auth/login.

        Returns the parsed JSON dict on HTTP 200, None otherwise.

        Override this method in tests to avoid real network calls.
        """
        api_url = os.environ.get("LABDATA_API_URL", _LABDATA_API_URL_DEFAULT).rstrip("/")
        url = f"{api_url}/v1/auth/login"
        body = json.dumps({"email": email, "password": password}).encode("utf-8")

        client = AsyncHTTPClient()
        try:
            req = HTTPRequest(
                url,
                method="POST",
                body=body,
                headers={"Content-Type": "application/json"},
            )
            # raise_error is a fetch() argument, not an HTTPRequest field; pass it
            # here so non-200 responses (bad creds) return a response we inspect
            # rather than raising.
            response = await client.fetch(req, raise_error=False)
        except Exception:
            log.exception("LabDataAuthenticator: HTTP call to %s failed", url)
            return None

        if response.code != 200:
            log.warning(
                "LabDataAuthenticator: login failed for %s (HTTP %s)",
                email,
                response.code,
            )
            return None

        try:
            return json_decode(response.body)
        except Exception:
            log.exception("LabDataAuthenticator: could not parse login response")
            return None

    # ------------------------------------------------------------------
    # Username normalisation
    # ------------------------------------------------------------------

    @staticmethod
    def normalize_username(email: str) -> str:  # type: ignore[override]
        """
        Derive a valid Linux username from an email address.

        Steps:
        1. lowercase the whole address
        2. take the local part (before '@')
        3. replace any character not in [a-z0-9_-] with '_'

        Examples:
            'jane.doe@nus.edu.sg'  -> 'jane_doe'
            'Jane.Doe@nus.edu.sg'  -> 'jane_doe'
            'user+tag@example.com' -> 'user_tag'
            'alice@lab.local'      -> 'alice'
        """
        local = email.lower().split("@")[0]
        return _USERNAME_SAFE.sub("_", local)
