"""
jupyterhub_config.py — portable JupyterHub configuration for the Lab Data system.

Spawner selection:
  JUPYTERHUB_DEV=1  → SimpleLocalProcessSpawner  (dev container)
  (unset)           → default spawner; TLJH overrides with SystemdSpawner
                      via its own config.d/ (see commented block below).

Authentication:
  LabDataAuthenticator — delegates to the Go API at LABDATA_API_URL.

Architecture references: §18 (critical interactive layer), §22 (security/perms).
"""
from __future__ import annotations

import os
import sys

# ---------------------------------------------------------------------------
# Locate labdata_authenticator alongside this file (dev container + TLJH)
# ---------------------------------------------------------------------------
_here = os.path.dirname(os.path.abspath(__file__))
if _here not in sys.path:
    sys.path.insert(0, _here)

from labdata_authenticator import LabDataAuthenticator  # noqa: E402

# ---------------------------------------------------------------------------
# Authentication
# ---------------------------------------------------------------------------
c.JupyterHub.authenticator_class = LabDataAuthenticator  # type: ignore[name-defined]

# API endpoint for credential validation (passed into LabDataAuthenticator via env).
# The authenticator reads os.environ["LABDATA_API_URL"] at call time, so setting
# the env var here is the correct injection point for non-container deployments.
_labdata_api_url = os.environ.get("LABDATA_API_URL", "http://localhost:8080")
os.environ.setdefault("LABDATA_API_URL", _labdata_api_url)

# ---------------------------------------------------------------------------
# Spawner selection
# ---------------------------------------------------------------------------
_dev_mode = bool(os.environ.get("JUPYTERHUB_DEV"))

if _dev_mode:
    from jupyterhub.spawner import SimpleLocalProcessSpawner
    c.JupyterHub.spawner_class = SimpleLocalProcessSpawner  # type: ignore[name-defined]

    # SimpleLocalProcessSpawner runs every single-user server as the Hub's own
    # process user — there are NO per-user OS accounts. LocalAuthenticator.add_user
    # otherwise refuses to register a Hub user unless a matching system account
    # exists (or create_system_users is on), which breaks both first browser
    # login and the API's "Generate connection link" (admin create-user + token).
    # Treat all users as present in dev so Hub-side user records work without
    # useradd. Production (TLJH/SystemdSpawner) is unaffected — _dev_mode is off.
    from jupyterhub.auth import LocalAuthenticator
    LocalAuthenticator.system_user_exists = lambda self, user: True  # type: ignore[assignment]

    # The dev container runs the Hub — and therefore the SimpleLocalProcessSpawner
    # single-user servers — as root. Jupyter Server aborts on startup as root
    # unless --allow-root is passed, which otherwise makes every spawn time out
    # ("server never showed up"). Production runs servers as real users, so this
    # only applies in dev.
    c.Spawner.args = ["--allow-root"]  # type: ignore[name-defined]
# else: leave spawner_class at its default (DockerSpawner or SystemdSpawner
# injected by TLJH's config.d — see the commented block at the bottom).

# ---------------------------------------------------------------------------
# Per-user resource limits (§18 operational requirements)
# ---------------------------------------------------------------------------
c.Spawner.mem_limit = "2G"   # type: ignore[name-defined]
c.Spawner.cpu_limit = 2      # type: ignore[name-defined]

# ---------------------------------------------------------------------------
# Default URL — open JupyterLab directly
# ---------------------------------------------------------------------------
c.Spawner.default_url = "/lab"  # type: ignore[name-defined]

# ---------------------------------------------------------------------------
# Environment variables injected into every single-user server
# These give notebooks access to the lab data filesystem and read-only DB.
# ---------------------------------------------------------------------------
c.Spawner.environment = {  # type: ignore[name-defined]
    # Filesystem root — notebooks do open(LABDATA_ROOT + "/raw/...") etc.
    "LABDATA_ROOT":        os.environ.get("LABDATA_ROOT",        "/srv/labdata"),
    # Read-only Postgres connection (labdata_nb / labdata_readonly role).
    "LABDATA_READONLY_URL": os.environ.get("LABDATA_READONLY_URL", ""),
    # Full DATABASE_URL for admin/dev use (empty in prod notebooks by design).
    "DATABASE_URL":        os.environ.get("DATABASE_URL",        ""),
}

# ---------------------------------------------------------------------------
# Idle-culler service — shut down kernels / servers that have been idle
# (architecture §18: "restrict memory/CPU per user if needed")
# ---------------------------------------------------------------------------
c.JupyterHub.services = [  # type: ignore[name-defined]
    {
        "name": "idle-culler",
        "admin": True,
        "command": [
            sys.executable,
            "-m",
            "jupyterhub_idle_culler",
            "--timeout=3600",        # cull kernels idle for 1 h
            "--cull-every=600",      # check every 10 min
            "--remove-named-servers",
            "--cull-users=False",    # keep user records; only stop servers
        ],
    }
]

# ---------------------------------------------------------------------------
# Admin API service token for the lab-data Go API. This powers the web app's
# "Generate connection link" (VS Code) flow: the API authenticates to the Hub
# with this token to ensure the user exists and mint a short-lived per-user
# token. Set the SAME value as JUPYTERHUB_ADMIN_TOKEN on the API server. The
# service is only registered when JUPYTERHUB_API_ADMIN_TOKEN is provided, so the
# default dev Hub is unaffected if you don't use the IDE flow.
# ---------------------------------------------------------------------------
_api_admin_token = os.environ.get("JUPYTERHUB_API_ADMIN_TOKEN", "")
if _api_admin_token:
    c.JupyterHub.services.append(  # type: ignore[name-defined]
        {"name": "labdata-api", "api_token": _api_admin_token}
    )
    c.JupyterHub.load_roles = [  # type: ignore[name-defined]
        {
            "name": "labdata-api-minter",
            "services": ["labdata-api"],
            # admin:users -> create users; tokens -> mint per-user API tokens.
            "scopes": ["admin:users", "tokens", "read:users", "admin:servers"],
        }
    ]

# ---------------------------------------------------------------------------
# ── PRODUCTION (TLJH) SystemdSpawner block ──────────────────────────────────
#
# TLJH injects its own config.d entries; you do NOT normally set these here.
# The block below is provided for reference when setting up a non-TLJH prod
# deployment, or when overriding TLJH defaults in
# /opt/tljh/config/jupyterhub_config.d/labdata.py
#
# from systemdspawner import SystemdSpawner
# c.JupyterHub.spawner_class = SystemdSpawner
# c.SystemdSpawner.unit_name_template = "jupyter-{USERNAME}"
#
# # Create a matching system user + home dir when a new user first logs in.
# c.LocalAuthenticator.create_system_users = True
#
# # Resource limits enforced by systemd cgroup.
# c.SystemdSpawner.mem_limit = "2G"
# c.SystemdSpawner.cpu_limit = 2.0
#
# # Each user's server runs as that system user; scratch is their home dir
# # or /srv/labdata/scratch/<username> (see TLJH.md §22 ownership model).
# c.SystemdSpawner.username_template = "{USERNAME}"
# ────────────────────────────────────────────────────────────────────────────
