"""
Tests for labdata.catalog — read-only catalog queries.

Requires Postgres at TEST_DATABASE_URL.  Skips cleanly if unreachable.

Fixture strategy mirrors test_pipeline_integration.py:
  - module-level: create test DB, apply ALL migrations, apply readonly_role.sql
    with a dev password, set LABDATA_READONLY_URL to the labdata_nb DSN, call
    seed_demo() to populate.
  - each test queries via catalog.* API and checks seeded rows are returned.
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
# Connection helpers (same pattern as test_pipeline_integration.py)
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


pytestmark = pytest.mark.skipif(
    not _pg_reachable(),
    reason="Postgres not reachable at TEST_DATABASE_URL",
)

# Import migration helpers only if reachable
if _pg_reachable():
    from tests.test_pipeline_integration import _apply_migrations


# ---------------------------------------------------------------------------
# Module-level fixture: DB + readonly role + seed
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def test_db():
    """Create test DB, migrate, apply readonly role, seed demo, yield conn."""
    db_name = _db_name_from_dsn(TEST_DSN)
    admin_dsn = _admin_dsn()

    # Create DB
    with psycopg.connect(admin_dsn, autocommit=True) as ac:
        exists = ac.execute(
            "SELECT 1 FROM pg_database WHERE datname = %s", (db_name,)
        ).fetchone()
        if not exists:
            ac.execute(f'CREATE DATABASE "{db_name}"')

    conn = psycopg.connect(TEST_DSN, autocommit=False, row_factory=dict_row)
    try:
        _apply_migrations(conn)

        # Apply readonly role via admin conn with autocommit (DO $$ blocks need this)
        apply_readonly_role(admin_dsn, db_name, _NB_PASSWORD)

        yield conn
    finally:
        conn.close()


@pytest.fixture(scope="module")
def seeded(test_db, tmp_path_factory):
    """Run seed_demo and return its result dict. Also sets env vars."""
    tmp = tmp_path_factory.mktemp("labdata_root_catalog")

    # Build the labdata_nb DSN
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

    yield result

    os.environ.pop("LABDATA_READONLY_URL", None)
    os.environ.pop("LABDATA_ROOT", None)
    catalog.reset()


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

class TestListRuns:
    def test_returns_dataframe(self, seeded):
        df = catalog.list_runs()
        assert len(df) > 0

    def test_seeded_run_in_list(self, seeded):
        df = catalog.list_runs()
        assert seeded["run_id"] in df["id"].values

    def test_filter_by_state(self, seeded):
        df = catalog.list_runs(state="published")
        assert len(df) > 0
        assert all(df["state"] == "published")

    def test_filter_by_sample_id(self, seeded):
        df = catalog.list_runs(sample_id=seeded["sample_id"])
        assert len(df) > 0
        assert all(df["sample_id"] == seeded["sample_id"])

    def test_filter_by_device_id(self, seeded):
        df = catalog.list_runs(device_id=seeded["device_id"])
        assert len(df) > 0

    def test_filter_by_measurement_type(self, seeded):
        df = catalog.list_runs(measurement_type="fet_transfer")
        assert len(df) > 0
        assert all(df["measurement_type"] == "fet_transfer")

    def test_limit(self, seeded):
        df = catalog.list_runs(limit=1)
        assert len(df) <= 1


class TestGetRun:
    def test_returns_dict(self, seeded):
        row = catalog.get_run(seeded["run_id"])
        assert isinstance(row, dict)
        assert row["id"] == seeded["run_id"]

    def test_contains_expected_fields(self, seeded):
        row = catalog.get_run(seeded["run_id"])
        assert "state" in row
        assert "sample_id" in row
        assert "device_id" in row
        assert row["state"] == "published"

    def test_raises_key_error_for_missing(self, seeded):
        with pytest.raises(KeyError):
            catalog.get_run("nonexistent-run-id-xyz")


class TestRunFiles:
    def test_returns_dataframe(self, seeded):
        df = catalog.run_files(seeded["run_id"])
        assert len(df) > 0

    def test_contains_seeded_file(self, seeded):
        df = catalog.run_files(seeded["run_id"])
        assert "iv_sweep.data" in df["name"].values


class TestPublishedResults:
    def test_returns_dataframe(self, seeded):
        df = catalog.published_results()
        assert len(df) > 0

    def test_seeded_result_present(self, seeded):
        df = catalog.published_results()
        assert seeded["published_result_id"] in df["id"].astype(str).values


class TestSamples:
    def test_returns_dataframe(self, seeded):
        df = catalog.samples()
        assert len(df) > 0

    def test_seeded_sample_present(self, seeded):
        df = catalog.samples()
        assert seeded["sample_id"] in df["id"].values


class TestDevices:
    def test_returns_dataframe(self, seeded):
        df = catalog.devices()
        assert len(df) > 0

    def test_filter_by_sample(self, seeded):
        df = catalog.devices(sample_id=seeded["sample_id"])
        assert len(df) > 0
        assert all(df["sample_id"] == seeded["sample_id"])

    def test_seeded_device_present(self, seeded):
        df = catalog.devices()
        assert seeded["device_id"] in df["id"].values


class TestReviewArtifact:
    def test_returns_dict(self, seeded):
        ra = catalog.review_artifact(seeded["run_id"])
        assert ra is not None
        assert isinstance(ra, dict)

    def test_contains_metrics(self, seeded):
        ra = catalog.review_artifact(seeded["run_id"])
        assert "metrics_json" in ra
        metrics = ra["metrics_json"]
        assert "on_off_ratio" in metrics

    def test_none_for_missing_run(self, seeded):
        ra = catalog.review_artifact("nonexistent-run-xyz")
        assert ra is None
