"""
labdata.duck — DuckDB helpers for JupyterHub notebooks (architecture §18).

connect() returns a DuckDB connection with the catalog database ATTACHed
READ-ONLY as schema 'catalog', so notebooks can do:

    SELECT * FROM catalog.v_runs

The postgres extension is INSTALLed and LOADed automatically.

DSN resolution: LABDATA_READONLY_URL → DATABASE_URL → hard default.
"""

from __future__ import annotations

import os

import duckdb

_DEFAULT_FALLBACK = "postgres://lab:lab@localhost:5432/labdata?sslmode=disable"


def _get_dsn() -> str:
    return (
        os.environ.get("LABDATA_READONLY_URL")
        or os.environ.get("DATABASE_URL")
        or _DEFAULT_FALLBACK
    )


def connect(dsn: str | None = None) -> duckdb.DuckDBPyConnection:
    """Return a DuckDB connection with the catalog ATTACHed READ-ONLY.

    Parameters
    ----------
    dsn : Postgres DSN to attach; defaults to LABDATA_READONLY_URL /
          DATABASE_URL / built-in dev default.

    Returns
    -------
    duckdb.DuckDBPyConnection
        In-memory DuckDB with 'catalog' schema pointing at the Postgres DB.
        Use catalog.v_runs, catalog.v_devices, etc. in SQL.

    Notes
    -----
    DuckDB's postgres extension is installed from its default extension
    repository on first call (requires internet on first use; cached
    afterwards in ~/.duckdb).  ATTACH uses TYPE postgres with READ_ONLY so
    notebooks cannot write to the production DB.
    """
    effective_dsn = dsn or _get_dsn()

    conn = duckdb.connect(":memory:")

    # INSTALL/LOAD the postgres scanner extension
    conn.execute("INSTALL postgres")
    conn.execute("LOAD postgres")

    # ATTACH the catalog database read-only as schema 'catalog'
    # DuckDB 1.x syntax: ATTACH '<dsn>' AS <alias> (TYPE postgres, READ_ONLY)
    attach_sql = (
        f"ATTACH '{effective_dsn}' AS catalog (TYPE postgres, READ_ONLY)"
    )
    conn.execute(attach_sql)

    return conn
