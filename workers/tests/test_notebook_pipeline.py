"""
tests/test_notebook_pipeline.py — notebook runner + Pipeline-A routing tests.

Two layers of tests:
  1. Unit tests for notebook_runner (no DB needed): execute notebooks directly
     against sample_data files, assert plots + metrics produced.
  2. Integration tests (skipped when Postgres is unreachable): stage VAC and
     CV/CF runs in the test DB and exercise handle_parse_run end-to-end.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import sys
import tempfile
import uuid
from pathlib import Path

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from labdata.notebook_runner import run_processing
from labdata.pipeline import _detect_family

# ---------------------------------------------------------------------------
# Paths to real sample data
# ---------------------------------------------------------------------------

REPO_ROOT = Path(__file__).parent.parent.parent
SAMPLE_DATA = REPO_ROOT / "sample_data" / "9D66P1" / "2026-06-10"

VAC_DATA_DIR = SAMPLE_DATA / "tiny_7" / "data"
CV_CF_DATA_DIR = (
    SAMPLE_DATA / "tiny_9" / "tiny_9_run_2026-06-10_12-39-34" / "data"
)

NOTEBOOKS_DIR = Path(__file__).parent.parent / "notebooks"

# ---------------------------------------------------------------------------
# Family detection unit tests (no I/O)
# ---------------------------------------------------------------------------

class TestDetectFamily:
    def test_vac_measurement_type(self):
        assert _detect_family("VAC", []) == "iv_breakdown"

    def test_iv_measurement_type(self):
        assert _detect_family("IV_sweep", []) == "iv_breakdown"

    def test_breakdown_measurement_type(self):
        assert _detect_family("breakdown_test", []) == "iv_breakdown"

    def test_cv_measurement_type(self):
        assert _detect_family("CV_sweep", []) == "impedance_cv_cf"

    def test_cf_measurement_type(self):
        assert _detect_family("CF_impedance", []) == "impedance_cv_cf"

    def test_impedance_in_files(self):
        files = [{"name": "tiny_9_cv_100kHz.csv"}]
        assert _detect_family("measurement", files) == "impedance_cv_cf"

    def test_cf_in_files(self):
        files = [{"name": "device_cf_0V_block01.csv"}]
        assert _detect_family("measurement", files) == "impedance_cv_cf"

    def test_fet_transfer_fallback(self):
        assert _detect_family("fet_transfer", []) == "fet_transfer"

    def test_unknown_measurement_type(self):
        assert _detect_family("some_other_measurement", []) == "fet_transfer"


# ---------------------------------------------------------------------------
# notebook_runner unit tests (no DB; skip if sample data missing)
# ---------------------------------------------------------------------------

@pytest.mark.skipif(
    not VAC_DATA_DIR.exists(),
    reason="VAC sample data not present",
)
class TestIvBreakdownNotebook:
    def test_produces_plot_and_metrics(self, tmp_path):
        result = run_processing(
            family="iv_breakdown",
            params={
                "run_id": "test-vac-unit",
                "data_dir": str(VAC_DATA_DIR),
                "out_dir": str(tmp_path),
                "area_um2": 6333.0,
                "thickness_nm": 2.5,
                "device_id": "tiny_7",
            },
            notebook_path=str(NOTEBOOKS_DIR / "iv_breakdown.ipynb"),
            out_dir=str(tmp_path),
        )

        # Plots
        assert len(result["plots"]) >= 1, "No PNG plots produced"
        for p in result["plots"]:
            assert Path(p).exists(), f"Plot not on disk: {p}"
            assert p.endswith(".png")

        # Executed notebook
        assert Path(result["notebook"]).exists()
        assert result["notebook"].endswith("processed.ipynb")

        # Metrics
        m = result["metrics"]
        assert "I_max" in m, f"Missing I_max in metrics: {m}"
        assert "I_min" in m
        assert m["I_max"] > 0
        assert m["I_min"] > 0
        assert m["I_max"] >= m["I_min"]

    def test_metrics_json_written_to_out_dir(self, tmp_path):
        run_processing(
            family="iv_breakdown",
            params={
                "run_id": "test-vac-metrics",
                "data_dir": str(VAC_DATA_DIR),
                "out_dir": str(tmp_path),
                "area_um2": 6333.0,
                "thickness_nm": 2.5,
                "device_id": "tiny_7",
            },
            notebook_path=str(NOTEBOOKS_DIR / "iv_breakdown.ipynb"),
            out_dir=str(tmp_path),
        )
        metrics_file = tmp_path / "metrics.json"
        assert metrics_file.exists(), "metrics.json not written"
        data = json.loads(metrics_file.read_text())
        assert "I_max" in data


@pytest.mark.skipif(
    not CV_CF_DATA_DIR.exists(),
    reason="CV/CF sample data not present",
)
class TestImpedanceCvCfNotebook:
    def test_produces_two_plots_and_metrics(self, tmp_path):
        result = run_processing(
            family="impedance_cv_cf",
            params={
                "run_id": "test-cv-unit",
                "data_dir": str(CV_CF_DATA_DIR),
                "out_dir": str(tmp_path),
                "area_um2": 6333.0,
                "thickness_nm": 2.5,
                "device_id": "tiny_9",
            },
            notebook_path=str(NOTEBOOKS_DIR / "impedance_cv_cf.ipynb"),
            out_dir=str(tmp_path),
        )

        # Expect CF and CV plots
        assert len(result["plots"]) >= 2, f"Expected ≥2 plots; got {result['plots']}"
        for p in result["plots"]:
            assert Path(p).exists(), f"Plot not on disk: {p}"

        # Executed notebook
        assert Path(result["notebook"]).exists()

        # Metrics
        m = result["metrics"]
        assert "C_max_F" in m, f"Missing C_max_F: {m}"
        assert m["C_max_F"] is not None and m["C_max_F"] > 0
        assert "eps_r_accumulation" in m
        assert m["eps_r_accumulation"] is not None and m["eps_r_accumulation"] > 0
        assert m["n_cf_points"] > 0
        assert m["n_cv_points"] > 0

    def test_eps_r_computed_correctly(self, tmp_path):
        """eps_r = C_max * d / (eps0 * A); result should be order ~1-100 for dielectrics."""
        result = run_processing(
            family="impedance_cv_cf",
            params={
                "run_id": "test-cv-epsr",
                "data_dir": str(CV_CF_DATA_DIR),
                "out_dir": str(tmp_path),
                "area_um2": 6333.0,
                "thickness_nm": 2.5,
                "device_id": "tiny_9",
            },
            notebook_path=str(NOTEBOOKS_DIR / "impedance_cv_cf.ipynb"),
            out_dir=str(tmp_path),
        )
        eps_r = result["metrics"].get("eps_r_accumulation")
        assert eps_r is not None
        # Sanity-check: eps_r for real dielectric should be positive and finite
        assert 0 < eps_r < 1e6, f"eps_r out of range: {eps_r}"


# ---------------------------------------------------------------------------
# Integration tests — require Postgres
# ---------------------------------------------------------------------------

TEST_DSN = os.environ.get(
    "TEST_DATABASE_URL",
    "postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable",
)
MIGRATIONS_DIR = Path(__file__).parent.parent.parent / "migrations"


def _admin_dsn() -> str:
    return re.sub(r"(/[^/?]+)(\?|$)", r"/postgres\2", TEST_DSN)


def _pg_reachable() -> bool:
    try:
        import psycopg
        conn = psycopg.connect(_admin_dsn(), connect_timeout=3)
        conn.close()
        return True
    except Exception:
        return False


_pg_available = _pg_reachable()

pytestmark_db = pytest.mark.skipif(
    not _pg_available,
    reason="Postgres not reachable at TEST_DATABASE_URL",
)

# Re-use migration helpers from test_pipeline_integration
if _pg_available:
    from tests.test_pipeline_integration import (  # type: ignore[import]
        _parse_goose_up_statements,
        _apply_migrations,
        _db_name_from_dsn,
        _seed_agent,
        _seed_job,
        AGENT_ID,
        SAMPLE_ID,
        DEVICE_ID,
        TRUNCATE_TABLES,
    )


# ---------------------------------------------------------------------------
# DB fixtures (only materialise when Postgres is available)
# ---------------------------------------------------------------------------

if _pg_available:
    import psycopg
    from psycopg.rows import dict_row

    @pytest.fixture(scope="module")
    def nb_test_db():
        """Create test DB, apply migrations, yield connection."""
        db_name = _db_name_from_dsn(TEST_DSN)
        admin_dsn = _admin_dsn()
        with psycopg.connect(admin_dsn, autocommit=True) as ac:
            if not ac.execute(
                "SELECT 1 FROM pg_database WHERE datname = %s", (db_name,)
            ).fetchone():
                ac.execute(f'CREATE DATABASE "{db_name}"')

        conn = psycopg.connect(TEST_DSN, autocommit=False, row_factory=dict_row)
        try:
            _apply_migrations(conn)
            yield conn
        finally:
            conn.close()

    @pytest.fixture(autouse=False)
    def clean_nb_tables(nb_test_db):
        for tbl in TRUNCATE_TABLES + ["notifications"]:
            try:
                nb_test_db.execute(f'TRUNCATE TABLE "{tbl}" CASCADE')
            except Exception:
                nb_test_db.rollback()
        nb_test_db.commit()
        yield
        for tbl in TRUNCATE_TABLES + ["notifications"]:
            try:
                nb_test_db.execute(f'TRUNCATE TABLE "{tbl}" CASCADE')
            except Exception:
                nb_test_db.rollback()
        nb_test_db.commit()


def _seed_run_nb(conn, run_id: str, measurement_type: str, state: str = "promoted") -> None:
    conn.execute(
        """
        INSERT INTO runs
            (id, manifest_hash, agent_id, measurement_type, completion_source,
             sample_id, device_id, meas_path, state, declared_at)
        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, now())
        """,
        (
            run_id,
            "nbhash_" + run_id[:8],
            AGENT_ID,
            measurement_type,
            "operator",
            SAMPLE_ID,
            DEVICE_ID,
            f"/data/runs/{SAMPLE_ID}/{DEVICE_ID}/{run_id}",
            state,
        ),
    )


def _seed_manifest_nb(conn, run_id: str, measurement_type: str, files: list) -> None:
    raw = {
        "run_id": run_id,
        "schema_version": 1,
        "completion_source": "operator",
        "declared_by": "test",
        "declared_at": "2026-06-11T00:00:00Z",
        "agent_id": AGENT_ID,
        "meas_path": f"/data/runs/{SAMPLE_ID}/{DEVICE_ID}/{run_id}",
        "sample_id": SAMPLE_ID,
        "device_id": DEVICE_ID,
        "measurement_type": measurement_type,
        "files": files,
    }
    conn.execute(
        """
        INSERT INTO manifests
            (run_id, manifest_hash, schema_version, completion_source,
             declared_by, declared_at, raw_json)
        VALUES (%s, %s, %s, %s, %s, now(), %s::jsonb)
        """,
        (
            run_id,
            "nbhash_" + run_id[:8],
            1,
            "operator",
            "test",
            json.dumps(raw),
        ),
    )


def _stage_vac_run(labdata_root: str, run_id: str) -> None:
    """Copy tiny_7 VAC data into the temp raw dir for this run_id."""
    dest = Path(labdata_root) / "raw" / SAMPLE_ID / DEVICE_ID / run_id / "data"
    dest.mkdir(parents=True, exist_ok=True)
    for f in VAC_DATA_DIR.iterdir():
        shutil.copy2(f, dest / f.name)


def _stage_cv_cf_run(labdata_root: str, run_id: str) -> None:
    """
    Copy tiny_9 CV/CF data into the temp raw dir with the expected subdir
    structure: raw/<sample>/<device>/<run_id>/<run_subdir>/data/
    """
    run_subdir = "tiny_9_run_2026-06-10_12-39-34"
    dest = Path(labdata_root) / "raw" / SAMPLE_ID / DEVICE_ID / run_id / run_subdir / "data"
    dest.mkdir(parents=True, exist_ok=True)
    for f in CV_CF_DATA_DIR.iterdir():
        shutil.copy2(f, dest / f.name)


# ---------------------------------------------------------------------------
# Integration tests
# ---------------------------------------------------------------------------

@pytestmark_db
@pytest.mark.skipif(
    not (_pg_available and VAC_DATA_DIR.exists()),
    reason="Postgres or VAC sample data not available",
)
class TestVACRunIntegration:
    def test_vac_handle_parse_run(self, nb_test_db, clean_nb_tables):
        """VAC run → notebook path → awaiting_review + plots + metrics."""
        import psycopg
        from labdata import db as dbmod
        from labdata.pipeline import handle_parse_run

        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "vac-" + str(uuid.uuid4())[:8]

            _seed_agent(nb_test_db)
            _seed_run_nb(nb_test_db, run_id, "VAC")
            _seed_manifest_nb(nb_test_db, run_id, "VAC", [
                {"name": "VAC_tiny_7.data", "bytes": 100, "sha256": "x"}
            ])
            _seed_job(nb_test_db, run_id)
            _stage_vac_run(labdata_root, run_id)
            nb_test_db.commit()

            job = dbmod.claim_job(nb_test_db, "test-nb-worker", ["parse_run"])
            assert job is not None
            assert job["run_id"] == run_id

            result = handle_parse_run(nb_test_db, job, labdata_root, "test-nb-worker")
            dbmod.complete_job(nb_test_db, job["id"], result)
            nb_test_db.commit()

            # State
            row = nb_test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row["state"] == "awaiting_review", f"state={row['state']}"

            # review_artifacts
            ra = nb_test_db.execute(
                "SELECT metrics_json, plots_json FROM review_artifacts WHERE run_id = %s",
                (run_id,),
            ).fetchone()
            assert ra is not None
            assert "I_max" in ra["metrics_json"]
            plots = ra["plots_json"]
            assert len(plots) >= 1
            for p in plots:
                assert Path(p).exists(), f"Plot not on disk: {p}"

            # analysis_artifacts — should have plot + notebook rows
            arts = nb_test_db.execute(
                "SELECT kind FROM analysis_artifacts WHERE run_id = %s ORDER BY kind",
                (run_id,),
            ).fetchall()
            kinds = {a["kind"] for a in arts}
            assert "plot" in kinds
            assert "notebook" in kinds

            # send_notification job enqueued
            notif_job = nb_test_db.execute(
                "SELECT payload_json FROM jobs WHERE run_id = %s AND job_type = 'send_notification'",
                (run_id,),
            ).fetchone()
            assert notif_job is not None
            payload = notif_job["payload_json"]
            assert payload["event_type"] == "awaiting_review"
            assert payload["run_id"] == run_id
            assert "photo_paths" in payload
            assert "notification_id" in payload


@pytestmark_db
@pytest.mark.skipif(
    not (_pg_available and CV_CF_DATA_DIR.exists()),
    reason="Postgres or CV/CF sample data not available",
)
class TestCVCFRunIntegration:
    def test_cvcf_handle_parse_run(self, nb_test_db, clean_nb_tables):
        """CV/CF run → notebook path → awaiting_review + cf+cv plots + metrics."""
        from labdata import db as dbmod
        from labdata.pipeline import handle_parse_run

        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "cv-" + str(uuid.uuid4())[:8]

            _seed_agent(nb_test_db)
            _seed_run_nb(nb_test_db, run_id, "CV_CF")
            _seed_manifest_nb(nb_test_db, run_id, "CV_CF", [
                {"name": "tiny_9_cf_0V_block01.csv", "bytes": 100, "sha256": "x"},
                {"name": "tiny_9_cv_100kHz_block02.csv", "bytes": 100, "sha256": "y"},
            ])
            _seed_job(nb_test_db, run_id)
            _stage_cv_cf_run(labdata_root, run_id)
            nb_test_db.commit()

            job = dbmod.claim_job(nb_test_db, "test-nb-worker", ["parse_run"])
            assert job is not None

            result = handle_parse_run(nb_test_db, job, labdata_root, "test-nb-worker")
            dbmod.complete_job(nb_test_db, job["id"], result)
            nb_test_db.commit()

            row = nb_test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row["state"] == "awaiting_review"

            ra = nb_test_db.execute(
                "SELECT metrics_json, plots_json FROM review_artifacts WHERE run_id = %s",
                (run_id,),
            ).fetchone()
            assert ra is not None
            m = ra["metrics_json"]
            assert "C_max_F" in m, f"Missing C_max_F: {m}"
            plots = ra["plots_json"]
            assert len(plots) >= 2, f"Expected ≥2 plots: {plots}"

            # send_notification payload shape
            notif_job = nb_test_db.execute(
                "SELECT payload_json FROM jobs WHERE run_id = %s AND job_type = 'send_notification'",
                (run_id,),
            ).fetchone()
            assert notif_job is not None
            payload = notif_job["payload_json"]
            assert "notification_id" in payload
            assert payload["event_type"] == "awaiting_review"
            assert "CV_CF" in payload["message"]
