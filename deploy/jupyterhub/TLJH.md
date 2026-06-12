# TLJH Production Runbook — Lab Data JupyterHub

Architecture §18 (critical interactive layer) · §22 (security / permissions).

---

## 1. Prerequisites

- Ubuntu 22.04 LTS server with Python 3.10+, `curl`, and `sudo` access.
- Postgres running and migrations applied (see `/migrations/`).
- `readonly_role.sql` ready with the notebook password.
- The `labdata` Python workers package at `/opt/workers` or installed in the TLJH environment.

---

## 2. Install The Littlest JupyterHub

```bash
curl -L https://tljh.jupyter.org/bootstrap.py | sudo python3 - \
    --admin labadmin \
    --user-requirements-txt-url ""   # we install below
```

Wait for the bootstrap to complete. TLJH installs at `/opt/tljh`.

Verify:

```bash
sudo tljh-config show
journalctl -u jupyterhub --since "5 minutes ago" -f
```

---

## 3. Install the shared Python environment

TLJH manages a system-wide environment at `/opt/tljh/user/`. Install the
scientific stack and the `labdata` package there so every user's kernel has it.

```bash
sudo /opt/tljh/user/bin/pip install --no-cache-dir \
    jupyterlab \
    jupyterhub-idle-culler \
    duckdb \
    pandas \
    pyarrow \
    matplotlib \
    numpy \
    "psycopg[binary]"

# Install the labdata workers package (editable so updates take effect on next
# kernel restart without reinstalling).
sudo /opt/tljh/user/bin/pip install --no-cache-dir -e /opt/workers
```

---

## 4. Deploy the authenticator

Copy the authenticator to `/srv/jupyterhub/`:

```bash
sudo mkdir -p /srv/jupyterhub
sudo cp /path/to/repo/deploy/jupyterhub/labdata_authenticator.py \
         /srv/jupyterhub/labdata_authenticator.py
sudo chown root:root /srv/jupyterhub/labdata_authenticator.py
sudo chmod 644 /srv/jupyterhub/labdata_authenticator.py
```

---

## 5. Drop the config.d snippet

TLJH merges all files under `/opt/tljh/config/jupyterhub_config.d/`.
Create `/opt/tljh/config/jupyterhub_config.d/labdata.py`:

```python
# /opt/tljh/config/jupyterhub_config.d/labdata.py
# Lab Data authenticator + SystemdSpawner settings.
# Merged by TLJH on hub start.

import os, sys

# Ensure the authenticator module is importable.
_jhub_dir = "/srv/jupyterhub"
if _jhub_dir not in sys.path:
    sys.path.insert(0, _jhub_dir)

from labdata_authenticator import LabDataAuthenticator

# ── Authentication ──────────────────────────────────────────────────────────
c.JupyterHub.authenticator_class = LabDataAuthenticator
c.LabDataAuthenticator.admin_users = set()  # managed via global_role in the API

# LABDATA_API_URL is set in the TLJH environment or here:
os.environ.setdefault("LABDATA_API_URL", "http://localhost:8080")

# ── Spawner ─────────────────────────────────────────────────────────────────
# TLJH already loads SystemdSpawner; these settings override defaults.
c.SystemdSpawner.unit_name_template = "jupyter-{USERNAME}"
c.SystemdSpawner.username_template = "{USERNAME}"

# Create the system user + home dir on first login (§22 Unix ownership model).
c.LocalAuthenticator.create_system_users = True

# ── Resource limits (§18) ────────────────────────────────────────────────────
c.Spawner.mem_limit = "2G"
c.Spawner.cpu_limit = 2.0

# ── Default URL ──────────────────────────────────────────────────────────────
c.Spawner.default_url = "/lab"

# ── Environment injected into every single-user server ──────────────────────
c.Spawner.environment = {
    "LABDATA_ROOT":         "/srv/labdata",
    "LABDATA_READONLY_URL": os.environ.get("LABDATA_READONLY_URL", ""),
    "DATABASE_URL":         "",   # intentionally empty for notebook users
}

# ── Idle culler ──────────────────────────────────────────────────────────────
import sys as _sys
c.JupyterHub.services = [
    {
        "name": "idle-culler",
        "admin": True,
        "command": [
            _sys.executable, "-m", "jupyterhub_idle_culler",
            "--timeout=3600",
            "--cull-every=600",
            "--remove-named-servers",
            "--cull-users=False",
        ],
    }
]
```

Apply and restart:

```bash
sudo tljh-config reload
```

---

## 6. Apply the read-only SQL role

Run this **after** every migration (idempotent):

```bash
NB_PASSWORD="$(< /etc/labdata/nb_password)"   # or your secret manager
sed "s/__NB_PASSWORD__/${NB_PASSWORD}/" \
    /path/to/repo/deploy/jupyterhub/readonly_role.sql \
    | psql "$DATABASE_URL"
```

The `labdata_nb` login user inherits `labdata_readonly` which has SELECT only
on the catalog views (`v_samples`, `v_runs`, `v_run_files`, …).
No access to base tables, agents, jobs, or manifests.

---

## 7. Unix ownership model (§22)

```
/srv/labdata/
  raw/              owner: labdata-ingest   group: labdata-readers   mode: 0444/0555
  published/        owner: labdata-worker   group: labdata-readers   mode: 0444/0555
  processed/        owner: labdata-worker   group: labdata-readers   mode: 0444/0555
  manifests/        owner: labdata-ingest   group: labdata-readers   mode: 0444/0555
  scratch/<user>/   owner: <user>           group: <user>            mode: 0700
  notebooks/
    shared/         owner: labadmin         group: labdata-readers   mode: 0775
    teaching/       owner: labadmin         group: labdata-readers   mode: 0775
    examples/       owner: labadmin         group: labdata-readers   mode: 0775
```

Create the group and add the ingest service account:

```bash
sudo groupadd labdata-readers
sudo useradd  -r -s /sbin/nologin labdata-ingest
sudo useradd  -r -s /sbin/nologin labdata-worker
sudo usermod -aG labdata-readers labdata-nb     # if labdata-nb is also a system user

# Create the layout with correct ownership.
sudo mkdir -p /srv/labdata/{raw,processed,published,manifests,scratch}
sudo chown labdata-ingest:labdata-readers /srv/labdata/raw /srv/labdata/manifests
sudo chown labdata-worker:labdata-readers /srv/labdata/processed /srv/labdata/published
sudo chmod 0555 /srv/labdata/raw /srv/labdata/published /srv/labdata/processed \
                /srv/labdata/manifests

# Notebooks — read-mostly for lab members, writable by admins.
sudo mkdir -p /srv/labdata/notebooks/{shared,teaching,examples}
sudo chown -R labadmin:labdata-readers /srv/labdata/notebooks
sudo chmod -R 0775 /srv/labdata/notebooks
sudo chmod g+s /srv/labdata/notebooks   # setgid so new files inherit labdata-readers

# Per-user scratch: created by TLJH/create_system_users on first login.
# For existing users, create manually:
# sudo mkdir -p /srv/labdata/scratch/<username>
# sudo chown <username>:<username> /srv/labdata/scratch/<username>
# sudo chmod 0700 /srv/labdata/scratch/<username>
```

Enforce immutability of raw files after promotion (called by ingest worker):

```bash
chmod -R a-w /srv/labdata/raw/<sample_id>/<device_id>/<run_id>/
```

---

## 8. Seed shared notebooks

```bash
# Copy starter notebooks from the repo into the shared area.
sudo cp -r /path/to/repo/notebooks/shared/   /srv/labdata/notebooks/shared/
sudo cp -r /path/to/repo/notebooks/teaching/ /srv/labdata/notebooks/teaching/
sudo cp -r /path/to/repo/notebooks/examples/ /srv/labdata/notebooks/examples/
sudo chown -R labadmin:labdata-readers /srv/labdata/notebooks/
sudo chmod -R 0664 /srv/labdata/notebooks/**/*.ipynb 2>/dev/null || true
```

---

## 9. Backup policy for JupyterHub content

| What | Back up? | Notes |
|------|----------|-------|
| `/srv/labdata/notebooks/shared`   | YES | Versioned starter notebooks |
| `/srv/labdata/notebooks/teaching` | YES | Lab training material |
| `/srv/labdata/notebooks/examples` | YES | Reference examples |
| `/srv/labdata/scratch/<user>/`    | NO  | Per-user exploratory work — not authoritative |
| TLJH config.d                    | YES | `/opt/tljh/config/jupyterhub_config.d/` |
| Hub DB (SQLite)                  | YES | `/opt/tljh/state/jupyterhub.sqlite` |

Suggested cron (daily rsync to backup host):

```bash
rsync -az --delete \
    /srv/labdata/notebooks/ \
    backup-host:/backups/labdata/notebooks/

rsync -az \
    /opt/tljh/config/ \
    /opt/tljh/state/jupyterhub.sqlite \
    backup-host:/backups/labdata/tljh/
```

---

## 10. Verify the deployment

```bash
# Hub is up.
systemctl is-active jupyterhub

# Authenticator can be imported.
sudo /opt/tljh/user/bin/python -c "from labdata_authenticator import LabDataAuthenticator; print('OK')"

# labdata package is available in the user env.
sudo /opt/tljh/user/bin/python -c "import labdata; print('labdata OK')"

# duckdb is available.
sudo /opt/tljh/user/bin/python -c "import duckdb; print('duckdb OK')"

# Read-only role exists.
psql "$DATABASE_URL" -c "\du labdata_readonly"
psql "$DATABASE_URL" -c "\du labdata_nb"

# Smoke-test the view grants (as the nb user):
psql "postgresql://labdata_nb:${NB_PASSWORD}@localhost/lab?sslmode=disable" \
    -c "SELECT count(*) FROM v_runs;"
```
