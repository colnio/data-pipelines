"""
Tests for labdata.duck — DuckDB catalog ATTACH helper.

Requires Postgres at TEST_DATABASE_URL.  Skips cleanly if unreachable.

The test connects DuckDB to the same test database that seed_demo seeds,
and queries catalog.v_runs to verify rows are accessible from DuckDB.
"""

from __future__ import annotations

import os
import re
import sys

import psycopg
from psycopg.rows import dict_row
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from labdata import seed as seedmod
import labdata.catalog as catalog
from tests.conftest import apply_readonly_role

# ---------------------------------------------------------------------------
# Connection helpers
# ---------------------------------------------------------------------------

TEST_DSN = os.environ.get(
    "TEST_DATABASE_URL",
    "postgres://lab:lab@localhost:5432/labdata_test_catalog?sslmode=disable",
)
_NB_PASSWORD = "test_nb_password_dev"


def _admin_dsn() -> str:
    return re.sub(r"(/[^/?]+)(\?|$)", r"/postgres\2", TEST_DSN)


def _pg_reachable() -> bool:
    try:
        conn = psycopg.connect(_admin_dsn(), connect_timeout=3)
        conn.close()
        return True
    except Exception:
        return False


def _db_name_from_dsn(dsn: str) -> str:
    m = re.search(r"/([^/?]+)(\?|$)", dsn)
    return m.group(1) if m else "labdata_test_catalog"


if _pg_reachable():
    from tests.test_pipeline_integration import _apply_migrations


pytestmark = pytest.mark.skipif(
    not _pg_reachable(),
    reason="Postgres not reachable at TEST_DATABASE_URL",
)


# ---------------------------------------------------------------------------
# Module fixture
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def test_db():
    db_name = _db_name_from_dsn(TEST_DSN)
    admin_dsn = _admin_dsn()
    with psycopg.connect(admin_dsn, autocommit=True) as ac:
        exists = ac.execute(
            "SELECT 1 FROM pg_database WHERE datname = %s", (db_name,)
        ).fetchone()
        if not exists:
            ac.execute(f'CREATE DATABASE "{db_name}"')

    conn = psycopg.connect(TEST_DSN, autocommit=False, row_factory=dict_row)
    try:
        _apply_migrations(conn)
        apply_readonly_role(admin_dsn, db_name, _NB_PASSWORD)
        yield conn
    finally:
        conn.close()


@pytest.fixture(scope="module")
def seeded(test_db, tmp_path_factory):
    tmp = tmp_path_factory.mktemp("labdata_root_duck")
    nb_dsn = re.sub(
        r"://[^@]+@",
        f"://labdata_nb:{_NB_PASSWORD}@",
        TEST_DSN,
    )
    os.environ["LABDATA_READONLY_URL"] = nb_dsn
    os.environ["LABDATA_ROOT"] = str(tmp)
    catalog.reset()
    result = seedmod.seed_demo(write_url=TEST_DSN, labdata_root=str(tmp))
    catalog.reset()
    yield result, nb_dsn
    os.environ.pop("LABDATA_READONLY_URL", None)
    os.environ.pop("LABDATA_ROOT", None)
    catalog.reset()


# ---------------------------------------------------------------------------
# DuckDB tests
# ---------------------------------------------------------------------------

class TestDuckConnect:
    def test_connect_returns_connection(self, seeded):
        """duck.connect() should return a DuckDB connection without error."""
        import labdata.duck as duck
        result, nb_dsn = seeded
        conn = duck.connect(dsn=nb_dsn)
        try:
            assert conn is not None
        finally:
            conn.close()

    def test_catalog_schema_accessible(self, seeded):
        """catalog.v_runs should be queryable via DuckDB."""
        import labdata.duck as duck
        result, nb_dsn = seeded
        conn = duck.connect(dsn=nb_dsn)
        try:
            rows = conn.execute("SELECT * FROM catalog.v_runs LIMIT 5").fetchall()
            assert isinstance(rows, list)
        finally:
            conn.close()

    def test_seeded_run_visible_in_duckdb(self, seeded):
        """The seeded run_id should appear in DuckDB catalog.v_runs."""
        import labdata.duck as duck
        result, nb_dsn = seeded
        run_id = result["run_id"]
        conn = duck.connect(dsn=nb_dsn)
        try:
            rows = conn.execute(
                "SELECT id FROM catalog.v_runs WHERE id = ?",
                [run_id],
            ).fetchall()
            assert len(rows) == 1
            assert rows[0][0] == run_id
        finally:
            conn.close()

    def test_v_devices_accessible(self, seeded):
        """catalog.v_devices should be queryable."""
        import labdata.duck as duck
        result, nb_dsn = seeded
        conn = duck.connect(dsn=nb_dsn)
        try:
            rows = conn.execute(
                "SELECT id FROM catalog.v_devices WHERE id = ?",
                [result["device_id"]],
            ).fetchall()
            assert len(rows) == 1
        finally:
            conn.close()

    def test_v_published_results_accessible(self, seeded):
        """catalog.v_published_results should be queryable."""
        import labdata.duck as duck
        result, nb_dsn = seeded
        pub_id = result["published_result_id"]
        conn = duck.connect(dsn=nb_dsn)
        try:
            rows = conn.execute(
                "SELECT id::text FROM catalog.v_published_results WHERE id::text = ?",
                [pub_id],
            ).fetchall()
            assert len(rows) == 1
        finally:
            conn.close()

    def test_default_dsn_uses_env(self, seeded):
        """connect() with no dsn arg uses LABDATA_READONLY_URL from env."""
        import labdata.duck as duck
        result, nb_dsn = seeded
        os.environ["LABDATA_READONLY_URL"] = nb_dsn
        conn = duck.connect()
        try:
            rows = conn.execute("SELECT 1 AS x").fetchall()
            assert rows[0][0] == 1
        finally:
            conn.close()
