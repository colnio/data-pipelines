# Lab Data Notebooks

JupyterHub starter notebooks for the lab data management system
(architecture §18 JupyterHub — critical interactive compute layer).

## Directory layout

```
notebooks/
  examples/          Read-only catalog and data exploration
    01_browse_catalog.ipynb
    02_plot_a_run.ipynb
    03_published_results.ipynb
    04_duckdb_sql.ipynb
  teaching/          Learning resources and promotion path demos
    parser_prototyping.ipynb
  shared/            Shared demonstrations and audit tools
    reproducibility_demo.ipynb
```

## Access model

### File system access

| Path | Permission |
|---|---|
| `/srv/labdata/raw/` | Read-only for all JupyterHub users |
| `/srv/labdata/published/` | Read-only for all JupyterHub users |
| `/srv/labdata/processed/` | Read-only (controlled) |
| `/srv/labdata/scratch/<username>/` | Writable by the owning user |
| `/srv/labdata/notebooks/` | Writable for shared project notebooks (by permission) |

Raw and published data are immutable after promotion. Notebooks cannot overwrite
raw data. Only the ingest service account (`labdata-ingest`) can write raw files.

### Database access

JupyterHub notebooks connect to Postgres as `labdata_nb`, which inherits the
`labdata_readonly` role.

The `labdata_readonly` role has **SELECT only** on the `v_*` catalog views.
It has **no access** to the base tables (`runs`, `jobs`, `manifests`, `agents`, etc.).

This means notebooks can read catalog metadata safely but cannot:
- Modify run states
- Insert or delete runs
- Access job queue internals, agent keys, or raw manifest JSON

Connection is via `LABDATA_READONLY_URL` (falls back to `DATABASE_URL`).

### labdata Python API

Notebooks use the `labdata` package installed in the shared JupyterHub environment:

- `labdata.catalog` — query `v_*` views; returns pandas DataFrames
- `labdata.store` — resolve file paths via `LABDATA_ROOT`, load/parse raw data
- `labdata.duck` — DuckDB connection with catalog ATTACHed read-only
- `labdata.seed` — dev/test seeder (not for production use)
- `labdata.parsers.fet_transfer` — FET transfer curve parser

## Read-only DB role

```sql
-- labdata_readonly: SELECT on v_* views only, no base-table access
-- labdata_nb: login user that inherits labdata_readonly
-- See deploy/jupyterhub/readonly_role.sql
```

Re-run `readonly_role.sql` after any migration that adds new views.

## Promotion rule

A notebook is a **sandbox** — an exploration and teaching tool. It is not a
production pipeline step.

```
exploratory notebook
  → reviewed Python module in workers/labdata/parsers/
  → table-driven tests in workers/tests/
  → pinned environment in workers/requirements.txt
  → job type registered in workers/labdata/pipeline.py
  → processing_environment + reproducibility receipt recorded in DB
```

**Never use a notebook as the source of a state transition** (`runs.state`,
published results, parameter versions). All production mutations go through
the Go API or the worker pipeline with a DB-audited transition function.

See `teaching/parser_prototyping.ipynb` for a worked example of this path.

## Environment setup

Notebooks require the shared JupyterHub Python environment or a local venv
that matches `workers/requirements.txt`.

Environment variables:
- `LABDATA_READONLY_URL` — Postgres DSN for read-only notebook DB access
- `DATABASE_URL` — fallback DSN (used if `LABDATA_READONLY_URL` is not set)
- `LABDATA_ROOT` — root path for raw/processed/published data (default `/srv/labdata`)
