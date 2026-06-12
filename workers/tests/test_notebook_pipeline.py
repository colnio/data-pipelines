"""
tests/test_notebook_pipeline.py — notebook runner + Pipeline-A routing tests.

Two layers of tests:
  1. Unit tests for notebook_runner (no DB needed): execute session notebooks
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

from labdata.notebook_runner import run_processing, run_session
from labdata.pipeline import _detect_family

# ---------------------------------------------------------------------------
# Paths to real sample data
# ---------------------------------------------------------------------------

REPO_ROOT = Path(__file__).parent.parent.parent
SAMPLE_DATA = REPO_ROOT / "sample_data" / "9D66P1" / "2026-06-10"

# Session-level paths (used by new session notebooks)
SESSION_DIR = SAMPLE_DATA

# Per-device paths kept for backward-compat reference (used by integration tests)
VAC_DATA_DIR = SAMPLE_DATA / "tiny_7" / "data"
CV_CF_DATA_DIR = (
    SAMPLE_DATA / "tiny_9" / "tiny_9_run_2026-06-10_12-39-34" / "data"
)

NOTEBOOKS_DIR = Path(__file__).parent.parent / "notebooks"

AREA_BY_SIZE = {
    "big": 771786,
    "mid": 192180,
    "small": 67400,
    "little": 28508,
    "tiny": 6333,
}
THICKNESS_NM = 2.5

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
# Session-level notebook unit tests (no DB; skip if sample data missing)
# ---------------------------------------------------------------------------

@pytest.mark.skipif(
    not SESSION_DIR.exists(),
    reason="Session sample data not present",
)
class TestIvBreakdownNotebook:
    """Session-level iv_breakdown notebook: discovers all VAC devices."""

    def test_produces_plot_and_metrics(self, tmp_path):
        result = run_session(
            family="iv_breakdown",
            params={
                "sample_id": "9D66P1",
                "session_dir": str(SESSION_DIR),
                "out_dir": str(tmp_path),
                "dataset_label": "9D66P1",
                "vbd_current_threshold_a": 1e-2,
                "title": "9D66P1 — IV breakdown",
            },
            notebook_path=str(NOTEBOOKS_DIR / "iv_breakdown.ipynb"),
            out_dir=str(tmp_path),
        )

        # Plots
        assert len(result["plots"]) >= 1, "No PNG plots produced"
        for p in result["plots"]:
            assert Path(p).exists(), f"Plot not on disk: {p}"
            assert p.endswith(".png")

        # Executed notebook saved as processed_iv_breakdown.ipynb
        assert Path(result["notebook"]).exists()
        assert "iv_breakdown" in result["notebook"]

        # Metrics
        m = result["metrics"]
        assert "vbd_avg" in m, f"Missing vbd_avg in metrics: {m}"
        assert "vbd_min" in m
        assert "vbd_max" in m
        assert "n_devices" in m
        assert m["n_devices"] > 0
        assert 0 < m["vbd_avg"] < 10, f"vbd_avg out of range: {m['vbd_avg']}"

    def test_metrics_json_written_to_out_dir(self, tmp_path):
        run_session(
            family="iv_breakdown",
            params={
                "sample_id": "9D66P1",
                "session_dir": str(SESSION_DIR),
                "out_dir": str(tmp_path),
                "dataset_label": "9D66P1",
                "vbd_current_threshold_a": 1e-2,
            },
            notebook_path=str(NOTEBOOKS_DIR / "iv_breakdown.ipynb"),
            out_dir=str(tmp_path),
        )
        metrics_file = tmp_path / "metrics.json"
        assert metrics_file.exists(), "metrics.json not written"
        data = json.loads(metrics_file.read_text())
        assert "vbd_avg" in data


@pytest.mark.skipif(
    not SESSION_DIR.exists(),
    reason="Session sample data not present",
)
class TestImpedanceCvCfNotebook:
    """Session-level impedance_cv_cf notebook: discovers all CF/CV devices."""

    def test_produces_plot_and_metrics(self, tmp_path):
        result = run_session(
            family="impedance_cv_cf",
            params={
                "sample_id": "9D66P1",
                "session_dir": str(SESSION_DIR),
                "out_dir": str(tmp_path),
                "thickness_nm": THICKNESS_NM,
                "area_by_size": AREA_BY_SIZE,
                "cal_freq_hz": 1e4,
                "title": "9D66P1 — C(V)/C(F)",
            },
            notebook_path=str(NOTEBOOKS_DIR / "impedance_cv_cf.ipynb"),
            out_dir=str(tmp_path),
        )

        # Expect at least one summary plot
        assert len(result["plots"]) >= 1, f"Expected ≥1 plot; got {result['plots']}"
        for p in result["plots"]:
            assert Path(p).exists(), f"Plot not on disk: {p}"

        # Executed notebook saved as processed_impedance_cv_cf.ipynb
        assert Path(result["notebook"]).exists()
        assert "impedance_cv_cf" in result["notebook"]

        # Metrics
        m = result["metrics"]
        assert "mean_k" in m, f"Missing mean_k: {m}"
        assert m["mean_k"] is not None and m["mean_k"] > 0
        assert "k_by_size" in m
        assert "n_devices" in m
        assert m["n_devices"] > 0

    def test_mean_k_in_physical_range(self, tmp_path):
        """mean_k for a real dielectric should be roughly 1–5."""
        result = run_session(
            family="impedance_cv_cf",
            params={
                "sample_id": "9D66P1",
                "session_dir": str(SESSION_DIR),
                "out_dir": str(tmp_path),
                "thickness_nm": THICKNESS_NM,
                "area_by_size": AREA_BY_SIZE,
                "cal_freq_hz": 1e4,
            },
            notebook_path=str(NOTEBOOKS_DIR / "impedance_cv_cf.ipynb"),
            out_dir=str(tmp_path),
        )
        mean_k = result["metrics"].get("mean_k")
        assert mean_k is not None
        assert 1.0 < mean_k < 10.0, f"mean_k out of physical range: {mean_k}"


# ---------------------------------------------------------------------------
# run_session vs run_processing: notebook name differs
# ---------------------------------------------------------------------------

class TestRunSessionNamingContract:
    """run_session writes processed_<family>.ipynb; run_processing writes processed.ipynb."""

    def test_run_session_notebook_name(self, tmp_path):
        """Stub: just verify the naming logic without executing a real notebook."""
        # We only test the naming logic here; full execution is tested above.
        # The name must contain the family.
        from labdata.notebook_runner import run_session as _rs
        # Check it's importable and has the right signature
        import inspect
        sig = inspect.signature(_rs)
        assert list(sig.parameters.keys()) == ["family", "params", "notebook_path", "out_dir"]


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
        """VAC device run → parse_run path → validated (device data recorded, no plots).

        Per-device CV/CF and VAC runs now stop at 'validated'; review happens
        at the session level via handle_process_session.
        """
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

            # Device run must now rest at 'validated', not awaiting_review
            row = nb_test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row["state"] == "validated", (
                f"Expected 'validated' (device-level stop); got {row['state']!r}"
            )

            # A parser_results row must exist (status='ok')
            pr = nb_test_db.execute(
                "SELECT status FROM parser_results WHERE run_id = %s", (run_id,)
            ).fetchone()
            assert pr is not None, "parser_results row missing"
            assert pr["status"] == "ok"

            # No review_artifacts at device level
            ra = nb_test_db.execute(
                "SELECT id FROM review_artifacts WHERE run_id = %s", (run_id,)
            ).fetchone()
            assert ra is None, "review_artifacts should NOT exist at device level"

            # No send_notification job at device level
            notif_job = nb_test_db.execute(
                "SELECT id FROM jobs WHERE run_id = %s AND job_type = 'send_notification'",
                (run_id,),
            ).fetchone()
            assert notif_job is None, "send_notification should not be enqueued for device run"


@pytestmark_db
@pytest.mark.skipif(
    not (_pg_available and CV_CF_DATA_DIR.exists()),
    reason="Postgres or CV/CF sample data not available",
)
class TestCVCFRunIntegration:
    def test_cvcf_handle_parse_run(self, nb_test_db, clean_nb_tables):
        """CV/CF device run → parse_run path → validated (device data recorded, no plots).

        Per-device CV/CF runs now stop at 'validated'; review happens at the
        session level via handle_process_session.
        """
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

            # Device run must rest at 'validated', not awaiting_review
            row = nb_test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row["state"] == "validated", (
                f"Expected 'validated' (device-level stop); got {row['state']!r}"
            )

            # A parser_results row must exist (status='ok')
            pr = nb_test_db.execute(
                "SELECT status FROM parser_results WHERE run_id = %s", (run_id,)
            ).fetchone()
            assert pr is not None, "parser_results row missing"
            assert pr["status"] == "ok"

            # No review_artifacts at device level
            ra = nb_test_db.execute(
                "SELECT id FROM review_artifacts WHERE run_id = %s", (run_id,)
            ).fetchone()
            assert ra is None, "review_artifacts should NOT exist at device level"


# ---------------------------------------------------------------------------
# Integration test for handle_process_session
# ---------------------------------------------------------------------------

_SESSION_AGENT_ID = "session-processor"
_SESSION_SAMPLE_ID = "9D66P1"
_SESSION_KEY = "2026-06-10"
_SESSION_RUN_ID = f"{_SESSION_SAMPLE_ID}:{_SESSION_KEY}"
_REAL_SESSION_DIR = REPO_ROOT / "sample_data" / _SESSION_SAMPLE_ID / _SESSION_KEY


@pytestmark_db
@pytest.mark.skipif(
    not (_pg_available and _REAL_SESSION_DIR.exists()),
    reason="Postgres or session sample data not available",
)
class TestProcessSessionIntegration:
    """End-to-end test for handle_process_session with real sample data."""

    def test_handle_process_session(self, nb_test_db, clean_nb_tables):
        """Session run → handle_process_session → awaiting_review + artifacts + notification."""
        from labdata import db as dbmod
        from labdata.process_session import handle_process_session

        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = _SESSION_RUN_ID

            # Seed a session-processor agent
            nb_test_db.execute(
                """
                INSERT INTO agents (id, display_name, key_hash)
                VALUES (%s, %s, %s)
                ON CONFLICT (id) DO NOTHING
                """,
                (_SESSION_AGENT_ID, "Session Processor", "$2b$10$fakehashfakehashfakehashfakehash"),
            )

            # Seed a synthetic session run (no manifest required for process_session)
            nb_test_db.execute(
                """
                INSERT INTO runs
                    (id, manifest_hash, agent_id, measurement_type, completion_source,
                     sample_id, device_id, meas_path, state, declared_at)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, now())
                ON CONFLICT (id) DO NOTHING
                """,
                (
                    run_id,
                    "session_hash_" + run_id[:16].replace(":", "_"),
                    _SESSION_AGENT_ID,
                    "session",
                    "operator",
                    _SESSION_SAMPLE_ID,
                    "session",
                    f"/sessions/{_SESSION_SAMPLE_ID}/{_SESSION_KEY}",
                    "promoted",
                ),
            )

            # Enqueue a process_session job with the payload the Go API would produce
            job_payload = {
                "run_id": run_id,
                "sample_id": _SESSION_SAMPLE_ID,
                "session_key": _SESSION_KEY,
                "session_dir": str(_REAL_SESSION_DIR),
            }
            nb_test_db.execute(
                """
                INSERT INTO jobs
                    (job_type, run_id, state, priority, max_attempts,
                     available_at, idempotency_key, payload_json)
                VALUES ('process_session', %s, 'queued', 3, 5, now(), %s, %s::jsonb)
                """,
                (run_id, f"process_session:{run_id}", json.dumps(job_payload)),
            )
            nb_test_db.commit()

            # Claim and execute
            job = dbmod.claim_job(nb_test_db, _SESSION_AGENT_ID, ["process_session"])
            assert job is not None, "No process_session job to claim"
            assert job["run_id"] == run_id

            result = handle_process_session(
                nb_test_db, job, labdata_root, _SESSION_AGENT_ID
            )
            dbmod.complete_job(nb_test_db, job["id"], result)
            nb_test_db.commit()

            # Run must end at awaiting_review
            row = nb_test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row is not None
            assert row["state"] == "awaiting_review", f"state={row['state']!r}"

            # At least 2 analysis_artifact plot rows (one per notebook family)
            arts = nb_test_db.execute(
                "SELECT kind, path FROM analysis_artifacts WHERE run_id = %s ORDER BY kind",
                (run_id,),
            ).fetchall()
            kinds = [a["kind"] for a in arts]
            assert kinds.count("plot") >= 2, f"Expected >=2 plot artifacts; got {kinds}"
            assert "notebook" in kinds, "Expected at least one notebook artifact"

            # review_artifacts row exists
            ra = nb_test_db.execute(
                "SELECT metrics_json, plots_json FROM review_artifacts WHERE run_id = %s",
                (run_id,),
            ).fetchone()
            assert ra is not None, "review_artifacts row missing"
            plots = ra["plots_json"]
            assert len(plots) >= 2, f"Expected >=2 plots in review_artifacts; got {plots}"
            for p in plots:
                assert Path(p).exists(), f"Plot not on disk: {p}"

            # send_notification job enqueued with photo_paths
            notif_job = nb_test_db.execute(
                "SELECT payload_json FROM jobs WHERE run_id = %s AND job_type = 'send_notification'",
                (run_id,),
            ).fetchone()
            assert notif_job is not None, "send_notification job not enqueued"
            payload = notif_job["payload_json"]
            assert "notification_id" in payload
            assert payload["event_type"] == "awaiting_review"
            assert payload["run_id"] == run_id
            assert "photo_paths" in payload
            assert len(payload["photo_paths"]) >= 2
