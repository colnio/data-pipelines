"""
labdata.catalog — read-only catalog access for JupyterHub notebooks.

Connects as labdata_nb via env LABDATA_READONLY_URL, falling back to
DATABASE_URL.  Queries ONLY the v_* views (architecture §18 §23).

Connection is lazy: a new connection is made on first use and cached in
the module.  Call labdata.catalog.reset() to close and re-open (e.g. in
tests between DB re-seeds).
"""

from __future__ import annotations

import os
from typing import Optional

import pandas as pd
import psycopg
from psycopg.rows import dict_row

# ---------------------------------------------------------------------------
# Connection management
# ---------------------------------------------------------------------------

_DEFAULT_FALLBACK = "postgres://lab:lab@localhost:5432/labdata?sslmode=disable"
_conn: psycopg.Connection | None = None


def _get_dsn() -> str:
    return (
        os.environ.get("LABDATA_READONLY_URL")
        or os.environ.get("DATABASE_URL")
        or _DEFAULT_FALLBACK
    )


def _connection() -> psycopg.Connection:
    global _conn
    if _conn is None or _conn.closed:
        _conn = psycopg.connect(_get_dsn(), row_factory=dict_row)
    return _conn


def reset() -> None:
    """Close the cached connection (next call re-opens it)."""
    global _conn
    if _conn is not None and not _conn.closed:
        try:
            _conn.close()
        except Exception:
            pass
    _conn = None


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _fetchall_df(sql: str, params: tuple = ()) -> pd.DataFrame:
    conn = _connection()
    with conn.cursor() as cur:
        cur.execute(sql, params)
        rows = cur.fetchall()
    if not rows:
        return pd.DataFrame()
    return pd.DataFrame(rows)


def _fetchone_dict(sql: str, params: tuple = ()) -> dict | None:
    conn = _connection()
    with conn.cursor() as cur:
        cur.execute(sql, params)
        return cur.fetchone()


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def list_runs(
    state: Optional[str] = None,
    sample_id: Optional[str] = None,
    device_id: Optional[str] = None,
    measurement_type: Optional[str] = None,
    limit: int = 200,
) -> pd.DataFrame:
    """Return runs from v_runs, optionally filtered.

    Parameters
    ----------
    state : optional state filter (exact match)
    sample_id : optional sample_id filter (exact match)
    device_id : optional device_id filter (exact match)
    measurement_type : optional measurement_type filter (exact match)
    limit : maximum rows returned (default 200)

    Returns
    -------
    pandas.DataFrame with one row per run, ordered by declared_at DESC.
    """
    clauses: list[str] = []
    params: list = []

    if state is not None:
        clauses.append("state = %s")
        params.append(state)
    if sample_id is not None:
        clauses.append("sample_id = %s")
        params.append(sample_id)
    if device_id is not None:
        clauses.append("device_id = %s")
        params.append(device_id)
    if measurement_type is not None:
        clauses.append("measurement_type = %s")
        params.append(measurement_type)

    where = ("WHERE " + " AND ".join(clauses)) if clauses else ""
    sql = f"SELECT * FROM v_runs {where} ORDER BY declared_at DESC LIMIT %s"
    params.append(limit)
    return _fetchall_df(sql, tuple(params))


def get_run(run_id: str) -> dict:
    """Return a single run row from v_runs as a dict.

    Raises KeyError if no run with that id exists.
    """
    row = _fetchone_dict("SELECT * FROM v_runs WHERE id = %s", (run_id,))
    if row is None:
        raise KeyError(f"run not found: {run_id!r}")
    return dict(row)


def run_files(run_id: str) -> pd.DataFrame:
    """Return run_files rows for *run_id* from v_run_files."""
    return _fetchall_df(
        "SELECT * FROM v_run_files WHERE run_id = %s ORDER BY name",
        (run_id,),
    )


def published_results(limit: int = 200) -> pd.DataFrame:
    """Return published results from v_published_results, newest first."""
    return _fetchall_df(
        "SELECT * FROM v_published_results ORDER BY published_at DESC LIMIT %s",
        (limit,),
    )


def samples() -> pd.DataFrame:
    """Return all samples from v_samples."""
    return _fetchall_df("SELECT * FROM v_samples ORDER BY id")


def devices(sample_id: Optional[str] = None) -> pd.DataFrame:
    """Return devices from v_devices, optionally filtered by sample_id."""
    if sample_id is not None:
        return _fetchall_df(
            "SELECT * FROM v_devices WHERE sample_id = %s ORDER BY id",
            (sample_id,),
        )
    return _fetchall_df("SELECT * FROM v_devices ORDER BY id")


def review_artifact(run_id: str) -> dict | None:
    """Return the latest v_review_artifacts row for *run_id*, or None."""
    row = _fetchone_dict(
        "SELECT * FROM v_review_artifacts WHERE run_id = %s ORDER BY id DESC LIMIT 1",
        (run_id,),
    )
    return dict(row) if row is not None else None
