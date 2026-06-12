"""
Tests for labdata.seed — dev/test seeding helpers.

Requires Postgres at TEST_DATABASE_URL.  Skips cleanly if unreachable.

Tests verify that seed_demo() inserts the expected rows and writes the
expected files to disk.
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path

import psycopg
from psycopg.rows import dict_row
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from labdata import seed as seedmod

# ---------------------------------------------------------------------------
# Connection helpers
# ---------------------------------------------------------------------------

TEST_DSN = os.environ.get(
    "TEST_DATABASE_URL",
    "postgres://lab:lab@localhost:5432/labdata_test_catalog?sslmode=disable",
)


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
# Module-level fixture
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
        yield conn
    finally:
        conn.close()


@pytest.fixture
def seeded_result(test_db, tmp_path):
    """Run seed_demo into tmp_path; return (result_dict, tmp_path)."""
    result = seedmod.seed_demo(write_url=TEST_DSN, labdata_root=str(tmp_path))
    return result, tmp_path


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

class TestSeedDemoReturnValue:
    def test_returns_dict(self, seeded_result):
        result, _ = seeded_result
        assert isinstance(result, dict)

    def test_required_keys(self, seeded_result):
        result, _ = seeded_result
        for key in ("run_id", "sample_id", "device_id", "raw_dir", "published_result_id"):
            assert key in result, f"missing key: {key}"

    def test_run_id_value(self, seeded_result):
        result, _ = seeded_result
        assert result["run_id"] == "demo-run-0001"

    def test_sample_id_value(self, seeded_result):
        result, _ = seeded_result
        assert result["sample_id"] == "DEMO1"

    def test_device_id_value(self, seeded_result):
        result, _ = seeded_result
        assert result["device_id"] == "demo_dev_1"


class TestSeedDemoFiles:
    def test_raw_file_created(self, seeded_result):
        result, root = seeded_result
        raw_file = Path(result["raw_dir"]) / "iv_sweep.data"
        assert raw_file.exists(), f"raw file missing: {raw_file}"

    def test_raw_file_has_data(self, seeded_result):
        result, root = seeded_result
        raw_file = Path(result["raw_dir"]) / "iv_sweep.data"
        content = raw_file.read_text()
        assert "V,I" in content

    def test_processed_plot_created(self, seeded_result):
        result, root = seeded_result
        proc_dir = (
            root / "processed" / "demo-run-0001" / "analysis_version_001"
        )
        plot = proc_dir / "transfer.png"
        assert plot.exists(), f"plot file missing: {plot}"

    def test_plot_is_valid_bytes(self, seeded_result):
        result, root = seeded_result
        proc_dir = (
            root / "processed" / "demo-run-0001" / "analysis_version_001"
        )
        plot = proc_dir / "transfer.png"
        assert plot.stat().st_size > 0

    def test_raw_dir_path_layout(self, seeded_result):
        result, root = seeded_result
        # raw dir should be raw/DEMO1/demo_dev_1/demo-run-0001
        expected = root / "raw" / "DEMO1" / "demo_dev_1" / "demo-run-0001"
        assert Path(result["raw_dir"]) == expected


class TestSeedDemoDbRows:
    def test_run_exists_in_db(self, test_db, seeded_result):
        result, _ = seeded_result
        row = test_db.execute(
            "SELECT state FROM runs WHERE id = %s", (result["run_id"],)
        ).fetchone()
        assert row is not None
        assert row["state"] == "published"

    def test_sample_exists(self, test_db, seeded_result):
        result, _ = seeded_result
        row = test_db.execute(
            "SELECT id FROM samples WHERE id = %s", (result["sample_id"],)
        ).fetchone()
        assert row is not None

    def test_device_exists(self, test_db, seeded_result):
        result, _ = seeded_result
        row = test_db.execute(
            "SELECT id, device_class FROM devices WHERE id = %s",
            (result["device_id"],),
        ).fetchone()
        assert row is not None
        assert row["device_class"] == "fet"

    def test_run_files_row(self, test_db, seeded_result):
        result, _ = seeded_result
        rows = test_db.execute(
            "SELECT name FROM run_files WHERE run_id = %s", (result["run_id"],)
        ).fetchall()
        names = [r["name"] for r in rows]
        assert "iv_sweep.data" in names

    def test_review_artifact_row(self, test_db, seeded_result):
        result, _ = seeded_result
        row = test_db.execute(
            "SELECT metrics_json FROM review_artifacts WHERE run_id = %s",
            (result["run_id"],),
        ).fetchone()
        assert row is not None
        assert "on_off_ratio" in row["metrics_json"]

    def test_published_result_row(self, test_db, seeded_result):
        result, _ = seeded_result
        row = test_db.execute(
            "SELECT run_id FROM published_results WHERE id = %s",
            (result["published_result_id"],),
        ).fetchone()
        assert row is not None
        assert row["run_id"] == result["run_id"]

    def test_published_artifact_row(self, test_db, seeded_result):
        result, _ = seeded_result
        row = test_db.execute(
            "SELECT kind FROM published_artifacts WHERE published_result_id = %s",
            (result["published_result_id"],),
        ).fetchone()
        assert row is not None
        assert row["kind"] == "plot"

    def test_idempotent_second_run(self, test_db, seeded_result):
        """seed_demo called twice should not raise (DELETE + re-INSERT)."""
        result, root = seeded_result
        result2 = seedmod.seed_demo(write_url=TEST_DSN, labdata_root=str(root))
        assert result2["run_id"] == result["run_id"]
