"""
Tests for labdata.store — path resolution and file loading.

Requires Postgres at TEST_DATABASE_URL (for load_run_data which calls
catalog.get_run).  Skips cleanly if unreachable.
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
import labdata.store as store
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
# Module fixture: create DB + seed
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
    tmp = tmp_path_factory.mktemp("labdata_root_store")
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
    yield result, str(tmp)
    os.environ.pop("LABDATA_READONLY_URL", None)
    os.environ.pop("LABDATA_ROOT", None)
    catalog.reset()


# ---------------------------------------------------------------------------
# Sanitization unit tests (no DB needed)
# ---------------------------------------------------------------------------

class TestSanitize:
    """Test the store._sanitize helper in isolation."""

    def test_plain_string(self):
        assert store._sanitize("DEMO1") == "DEMO1"

    def test_forward_slash_replaced(self):
        assert store._sanitize("a/b") == "a_b"

    def test_backslash_replaced(self):
        assert store._sanitize("a\\b") == "a_b"

    def test_empty_becomes_underscore(self):
        assert store._sanitize("") == "_"

    def test_dot_becomes_underscore(self):
        assert store._sanitize(".") == "_"

    def test_dotdot_becomes_underscore(self):
        assert store._sanitize("..") == "_"

    def test_whitespace_stripped(self):
        assert store._sanitize("  abc  ") == "abc"

    def test_none_becomes_underscore(self):
        assert store._sanitize(None) == "_"


class TestSanitizeOrUnknown:
    def test_none_becomes_unknown(self):
        assert store._sanitize_or_unknown(None) == "_unknown"

    def test_empty_becomes_unknown(self):
        assert store._sanitize_or_unknown("") == "_unknown"

    def test_plain_passes_through(self):
        assert store._sanitize_or_unknown("DEMO1") == "DEMO1"


# ---------------------------------------------------------------------------
# Path resolution tests
# ---------------------------------------------------------------------------

class TestRawDir:
    def test_basic_path(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        p = store.raw_dir("DEMO1", "demo_dev_1", "demo-run-0001")
        assert str(p).endswith("raw/DEMO1/demo_dev_1/demo-run-0001")

    def test_slash_in_sample_sanitized(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        p = store.raw_dir("SA/MPL", "dev1", "run1")
        assert "SA_MPL" in str(p)

    def test_none_sample_becomes_unknown(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        p = store.raw_dir(None, "dev1", "run1")
        assert "_unknown" in str(p)


class TestProcessedDir:
    def test_default_version(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        p = store.processed_dir("demo-run-0001")
        assert "analysis_version_001" in str(p)

    def test_custom_version(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        p = store.processed_dir("demo-run-0001", version=3)
        assert "analysis_version_003" in str(p)


class TestPublishedDir:
    def test_path_structure(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        p = store.published_dir(result["published_result_id"])
        assert "published" in str(p)
        assert result["published_result_id"] in str(p)


class TestRawFiles:
    def test_returns_list(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        catalog.reset()
        files = store.raw_files(result["run_id"])
        assert isinstance(files, list)

    def test_contains_seeded_file(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        catalog.reset()
        files = store.raw_files(result["run_id"])
        names = [f.name for f in files]
        assert "iv_sweep.data" in names


class TestLoadRunData:
    def test_returns_parse_result(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        catalog.reset()
        from labdata.parsers.fet_transfer import ParseResult
        pr = store.load_run_data(result["run_id"])
        assert isinstance(pr, ParseResult)
        assert pr.ok is True

    def test_parsed_rows(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        catalog.reset()
        pr = store.load_run_data(result["run_id"])
        assert pr.rows_actual == 5  # seed data has 5 rows

    def test_raises_for_missing_run(self, seeded):
        result, root = seeded
        os.environ["LABDATA_ROOT"] = root
        catalog.reset()
        with pytest.raises(KeyError):
            store.load_run_data("nonexistent-run-xyz")
