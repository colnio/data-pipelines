"""
Integration tests for the Pipeline-A worker.

Requires a Postgres instance at TEST_DATABASE_URL
(default: postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable).

If Postgres is unreachable the entire module is skipped.

Fixture strategy:
  - module-level: create test DB, apply ALL migrations (strip goose directives),
    connect and yield the connection.
  - function-level: truncate touched tables between tests so each test gets a
    clean slate without dropping/recreating the schema.

What is tested:
  1. Full worker iteration: seed run+manifest+raw file+job →
     handle_parse_run() → assert state='awaiting_review', review_artifacts row
     exists with metrics, job is 'succeeded', plot file exists on disk.
  2. Parser failure path: seed run with a corrupt data file →
     assert state='parser_failed', job is 'failed' or 'dead'.
"""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
import textwrap
import uuid
from pathlib import Path

import psycopg
from psycopg.rows import dict_row
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from labdata import db as dbmod
from labdata.pipeline import handle_parse_run

# ---------------------------------------------------------------------------
# Connection / DB-availability helpers
# ---------------------------------------------------------------------------

TEST_DSN = os.environ.get(
    "TEST_DATABASE_URL",
    "postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable",
)

MIGRATIONS_DIR = Path(__file__).parent.parent.parent / "migrations"


def _admin_dsn() -> str:
    """Return a DSN pointing at the 'postgres' DB for admin operations."""
    return re.sub(r"(/[^/?]+)(\?|$)", r"/postgres\2", TEST_DSN)


def _pg_reachable() -> bool:
    """Check if Postgres is reachable by connecting to the 'postgres' admin DB."""
    try:
        conn = psycopg.connect(_admin_dsn(), connect_timeout=3)
        conn.close()
        return True
    except Exception:
        return False


def _db_name_from_dsn(dsn: str) -> str:
    m = re.search(r"/([^/?]+)(\?|$)", dsn)
    if m:
        return m.group(1)
    return "labdata_test_py"


pytestmark = pytest.mark.skipif(
    not _pg_reachable(),
    reason="Postgres not reachable at TEST_DATABASE_URL",
)


# ---------------------------------------------------------------------------
# Migration parsing
# ---------------------------------------------------------------------------

def _parse_goose_up_statements(sql_text: str) -> list[str]:
    """
    Extract SQL statements from the '-- +goose Up' block of a migration file.

    Rules:
    - Find the block between '-- +goose Up' and '-- +goose Down' (or EOF).
    - Within that block, respect '-- +goose StatementBegin / StatementEnd'
      sections: treat their contents as a single statement (no semicolon split).
    - Outside those sections: split on semicolons, skip blanks/comments-only.
    """
    up_match = re.search(
        r"--\s*\+goose Up\s*\n(.*?)(?=--\s*\+goose Down|\Z)",
        sql_text,
        re.DOTALL | re.IGNORECASE,
    )
    if not up_match:
        return []
    up_block = up_match.group(1)

    statements = []
    pattern = re.compile(
        r"--\s*\+goose StatementBegin(.*?)--\s*\+goose StatementEnd",
        re.DOTALL | re.IGNORECASE,
    )
    last_end = 0
    for m in pattern.finditer(up_block):
        free = up_block[last_end:m.start()]
        statements.extend(_split_free_sql(free))
        stmt = m.group(1).strip()
        if stmt:
            statements.append(stmt)
        last_end = m.end()
    statements.extend(_split_free_sql(up_block[last_end:]))
    return statements


def _split_free_sql(text: str) -> list[str]:
    """
    Split free-form SQL text into statements on semicolons, ignoring semicolons
    that appear inside single-line comments (-- ...) or string literals.
    Returns only statements that contain at least one non-comment SQL token.
    """
    statements = []
    current: list[str] = []
    in_string = False
    string_char = ""
    i = 0
    while i < len(text):
        c = text[i]
        # Single-line comment: scan to end of line
        if not in_string and c == "-" and i + 1 < len(text) and text[i + 1] == "-":
            # Consume to newline
            end = text.find("\n", i)
            if end == -1:
                current.append(text[i:])
                i = len(text)
            else:
                current.append(text[i:end + 1])
                i = end + 1
            continue
        # String literal
        if not in_string and c in ("'", '"'):
            in_string = True
            string_char = c
            current.append(c)
            i += 1
            continue
        if in_string and c == string_char:
            in_string = False
            current.append(c)
            i += 1
            continue
        # Statement separator
        if not in_string and c == ";":
            stmt = "".join(current).strip()
            if _has_sql_tokens(stmt):
                statements.append(stmt)
            current = []
            i += 1
            continue
        current.append(c)
        i += 1
    # Trailing fragment
    stmt = "".join(current).strip()
    if _has_sql_tokens(stmt):
        statements.append(stmt)
    return statements


def _has_sql_tokens(text: str) -> bool:
    """Return True if text contains at least one non-comment non-whitespace SQL token."""
    for line in text.splitlines():
        stripped = line.strip()
        if stripped and not stripped.startswith("--"):
            return True
    return False


def _apply_migrations(conn: psycopg.Connection) -> None:
    """Apply all Up migrations in numeric order."""
    migration_files = sorted(MIGRATIONS_DIR.glob("*.sql"))
    for mf in migration_files:
        sql_text = mf.read_text()
        statements = _parse_goose_up_statements(sql_text)
        for stmt in statements:
            try:
                conn.execute(stmt)
            except Exception as exc:
                err = str(exc).lower()
                if "already exists" in err or "duplicate" in err:
                    conn.rollback()
                else:
                    raise
        conn.commit()


# ---------------------------------------------------------------------------
# Module-level fixture: DB setup
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def test_db():
    """Create the test DB if needed and apply migrations; yield connection."""
    db_name = _db_name_from_dsn(TEST_DSN)
    admin_dsn = _admin_dsn()

    with psycopg.connect(admin_dsn, autocommit=True) as admin_conn:
        exists = admin_conn.execute(
            "SELECT 1 FROM pg_database WHERE datname = %s", (db_name,)
        ).fetchone()
        if not exists:
            admin_conn.execute(f'CREATE DATABASE "{db_name}"')

    conn = psycopg.connect(TEST_DSN, autocommit=False, row_factory=dict_row)
    try:
        _apply_migrations(conn)
        yield conn
    finally:
        conn.close()


# ---------------------------------------------------------------------------
# Per-test cleanup fixture
# ---------------------------------------------------------------------------

TRUNCATE_TABLES = [
    "review_decisions",
    "review_artifacts",
    "analysis_artifacts",
    "parser_results",
    "run_state_transitions",
    "run_condition_labels",
    "run_files",
    "manifests",
    "instrument_metadata_raw",
    "jobs",
    "runs",
    "agents",
]


@pytest.fixture(autouse=True)
def clean_tables(test_db):
    """Truncate all seeded tables before each test."""
    for tbl in TRUNCATE_TABLES:
        try:
            test_db.execute(f'TRUNCATE TABLE "{tbl}" CASCADE')
        except Exception:
            test_db.rollback()
    test_db.commit()
    yield
    for tbl in TRUNCATE_TABLES:
        try:
            test_db.execute(f'TRUNCATE TABLE "{tbl}" CASCADE')
        except Exception:
            test_db.rollback()
    test_db.commit()


# ---------------------------------------------------------------------------
# Seed helpers
# ---------------------------------------------------------------------------

AGENT_ID = "test-agent-pipeline-a"
SAMPLE_ID = "TEST_SAMPLE_01"
DEVICE_ID = "test_device_01"


def _seed_agent(conn: psycopg.Connection) -> None:
    conn.execute(
        """
        INSERT INTO agents (id, display_name, key_hash)
        VALUES (%s, %s, %s)
        ON CONFLICT (id) DO NOTHING
        """,
        (AGENT_ID, "Test Agent", "$2b$10$fakehashfakehashfakehashfakehash"),
    )


def _seed_run(conn: psycopg.Connection, run_id: str, state: str = "promoted") -> None:
    conn.execute(
        """
        INSERT INTO runs
            (id, manifest_hash, agent_id, measurement_type, completion_source,
             sample_id, device_id, meas_path, state, declared_at)
        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, now())
        """,
        (
            run_id,
            "testhash_" + run_id[:8],
            AGENT_ID,
            "fet_transfer",
            "operator",
            SAMPLE_ID,
            DEVICE_ID,
            f"/data/runs/{SAMPLE_ID}/{DEVICE_ID}/{run_id}",
            state,
        ),
    )


def _seed_manifest(conn: psycopg.Connection, run_id: str, files: list[dict]) -> None:
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
        "measurement_type": "fet_transfer",
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
            "testhash_" + run_id[:8],
            1,
            "operator",
            "test",
            json.dumps(raw),
        ),
    )


def _seed_job(conn: psycopg.Connection, run_id: str) -> int:
    row = conn.execute(
        """
        INSERT INTO jobs
            (job_type, run_id, state, priority, max_attempts,
             available_at, idempotency_key, payload_json)
        VALUES ('parse_run', %s, 'queued', 0, 5, now(),
                %s, %s::jsonb)
        RETURNING id
        """,
        (run_id, f"parse_run:{run_id}", json.dumps({"run_id": run_id})),
    ).fetchone()
    return row["id"]


def _make_raw_file(labdata_root: str, run_id: str, filename: str, content: str) -> Path:
    p = Path(labdata_root) / "raw" / SAMPLE_ID / DEVICE_ID / run_id / filename
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(content)
    return p


_VALID_DATA = textwrap.dedent("""\
    # FET transfer sweep
    # nrows=10
    V,I
    -3.0,1.2e-12
    -2.0,3.4e-12
    -1.0,1.5e-11
     0.0,8.9e-10
     0.5,2.3e-8
     1.0,1.1e-6
     1.5,4.2e-5
     2.0,9.8e-4
     2.5,2.2e-3
     3.0,5.0e-3
""")

_INVALID_DATA = "this is not a data file\nno numbers here\n"


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

class TestFullWorkerIteration:
    def test_successful_parse_run(self, test_db):
        """End-to-end: seed → handle_parse_run → awaiting_review."""
        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "run-" + str(uuid.uuid4())
            data_file = "iv_sweep.data"

            _seed_agent(test_db)
            _seed_run(test_db, run_id, state="promoted")
            _seed_manifest(test_db, run_id, [{"name": data_file, "bytes": 100, "sha256": "x"}])
            _make_raw_file(labdata_root, run_id, data_file, _VALID_DATA)
            _seed_job(test_db, run_id)
            test_db.commit()

            job = dbmod.claim_job(test_db, "test-worker", ["parse_run"])
            assert job is not None, "no job claimed"
            assert job["run_id"] == run_id

            result = handle_parse_run(
                test_db, job, labdata_root, worker_id="test-worker"
            )
            dbmod.complete_job(test_db, job["id"], result)
            test_db.commit()

            # Run state must be 'awaiting_review'
            row = test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row is not None
            assert row["state"] == "awaiting_review", f"state={row['state']}"

            # review_artifacts row must exist with non-empty metrics
            ra = test_db.execute(
                "SELECT metrics_json, plots_json FROM review_artifacts WHERE run_id = %s",
                (run_id,),
            ).fetchone()
            assert ra is not None, "no review_artifacts row"
            metrics = ra["metrics_json"]
            assert "I_max" in metrics
            assert "I_min" in metrics
            assert "on_off_ratio" in metrics

            # Job must be succeeded
            job_row = test_db.execute(
                "SELECT state FROM jobs WHERE run_id = %s", (run_id,)
            ).fetchone()
            assert job_row["state"] == "succeeded"

            # Plot file must exist on disk
            plots = ra["plots_json"]
            assert len(plots) > 0
            assert Path(plots[0]).exists(), f"plot not found: {plots[0]}"

    def test_state_transitions_audit_trail(self, test_db):
        """All transitions should be recorded in run_state_transitions."""
        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "run-" + str(uuid.uuid4())
            data_file = "transfer.dat"

            _seed_agent(test_db)
            _seed_run(test_db, run_id, state="promoted")
            _seed_manifest(test_db, run_id, [{"name": data_file, "bytes": 50, "sha256": "y"}])
            _make_raw_file(labdata_root, run_id, data_file, _VALID_DATA)
            _seed_job(test_db, run_id)
            test_db.commit()

            job = dbmod.claim_job(test_db, "test-worker", ["parse_run"])
            result = handle_parse_run(test_db, job, labdata_root, "test-worker")
            dbmod.complete_job(test_db, job["id"], result)
            test_db.commit()

            transitions = test_db.execute(
                """
                SELECT from_state, to_state FROM run_state_transitions
                WHERE run_id = %s ORDER BY id
                """,
                (run_id,),
            ).fetchall()

            states = [(t["from_state"], t["to_state"]) for t in transitions]
            assert ("promoted", "parsing") in states
            assert ("parsing", "validated") in states
            assert ("validated", "processing") in states
            assert ("processing", "awaiting_review") in states

    def test_needs_metadata_state_also_accepted(self, test_db):
        """Run in 'needs_metadata' state should also transition to parsing."""
        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "run-" + str(uuid.uuid4())
            data_file = "iv.data"

            _seed_agent(test_db)
            _seed_run(test_db, run_id, state="needs_metadata")
            _seed_manifest(test_db, run_id, [{"name": data_file, "bytes": 50, "sha256": "z"}])
            _make_raw_file(labdata_root, run_id, data_file, _VALID_DATA)
            _seed_job(test_db, run_id)
            test_db.commit()

            job = dbmod.claim_job(test_db, "test-worker", ["parse_run"])
            result = handle_parse_run(test_db, job, labdata_root, "test-worker")
            dbmod.complete_job(test_db, job["id"], result)
            test_db.commit()

            row = test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row["state"] == "awaiting_review"

    def test_parser_failure_transitions_to_parser_failed(self, test_db):
        """A corrupt data file should → parser_failed; job is failed/dead."""
        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "run-" + str(uuid.uuid4())
            data_file = "corrupt.data"

            _seed_agent(test_db)
            _seed_run(test_db, run_id, state="promoted")
            _seed_manifest(test_db, run_id, [{"name": data_file, "bytes": 10, "sha256": "bad"}])
            _make_raw_file(labdata_root, run_id, data_file, _INVALID_DATA)
            _seed_job(test_db, run_id)
            test_db.commit()

            job = dbmod.claim_job(test_db, "test-worker", ["parse_run"])
            result = handle_parse_run(test_db, job, labdata_root, "test-worker")
            assert result.get("status") == "parser_failed"
            dbmod.fail_job(test_db, job["id"], result.get("error", "parse failed"))
            test_db.commit()

            row = test_db.execute(
                "SELECT state FROM runs WHERE id = %s", (run_id,)
            ).fetchone()
            assert row["state"] == "parser_failed"

            job_row = test_db.execute(
                "SELECT state FROM jobs WHERE run_id = %s", (run_id,)
            ).fetchone()
            assert job_row["state"] in ("failed", "dead", "queued")

    def test_analysis_artifact_inserted(self, test_db):
        """analysis_artifacts row with kind='plot' must exist after success."""
        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "run-" + str(uuid.uuid4())
            data_file = "sweep.data"

            _seed_agent(test_db)
            _seed_run(test_db, run_id, state="promoted")
            _seed_manifest(test_db, run_id, [{"name": data_file, "bytes": 50, "sha256": "aa"}])
            _make_raw_file(labdata_root, run_id, data_file, _VALID_DATA)
            _seed_job(test_db, run_id)
            test_db.commit()

            job = dbmod.claim_job(test_db, "test-worker", ["parse_run"])
            result = handle_parse_run(test_db, job, labdata_root, "test-worker")
            dbmod.complete_job(test_db, job["id"], result)
            test_db.commit()

            art = test_db.execute(
                "SELECT kind, sha256 FROM analysis_artifacts WHERE run_id = %s",
                (run_id,),
            ).fetchone()
            assert art is not None
            assert art["kind"] == "plot"
            assert art["sha256"] and len(art["sha256"]) == 64

    def test_parser_results_row_inserted(self, test_db):
        """parser_results row with status='ok' must exist after success."""
        with tempfile.TemporaryDirectory() as labdata_root:
            run_id = "run-" + str(uuid.uuid4())
            data_file = "iv.csv"

            _seed_agent(test_db)
            _seed_run(test_db, run_id, state="promoted")
            _seed_manifest(test_db, run_id, [{"name": data_file, "bytes": 50, "sha256": "bb"}])
            _make_raw_file(labdata_root, run_id, data_file, _VALID_DATA)
            _seed_job(test_db, run_id)
            test_db.commit()

            job = dbmod.claim_job(test_db, "test-worker", ["parse_run"])
            result = handle_parse_run(test_db, job, labdata_root, "test-worker")
            dbmod.complete_job(test_db, job["id"], result)
            test_db.commit()

            pr = test_db.execute(
                "SELECT status, parser_version, rows_actual FROM parser_results WHERE run_id = %s",
                (run_id,),
            ).fetchone()
            assert pr is not None
            assert pr["status"] == "ok"
            assert pr["rows_actual"] == 10  # _VALID_DATA has 10 data rows
            assert "fet_transfer" in pr["parser_version"]


# ---------------------------------------------------------------------------
# DB helper unit tests (require DB but no pipeline)
# ---------------------------------------------------------------------------

class TestDbHelpers:
    def test_transition_raises_state_mismatch(self, test_db):
        run_id = "run-" + str(uuid.uuid4())
        _seed_agent(test_db)
        _seed_run(test_db, run_id, state="promoted")
        test_db.commit()

        with pytest.raises(dbmod.StateMismatch):
            dbmod.transition(
                test_db, run_id,
                expected_from="parsing",  # wrong
                to="validated",
                actor_type="worker",
                actor_id="test",
                reason="test",
            )
        test_db.rollback()

    def test_transition_raises_illegal_transition(self, test_db):
        run_id = "run-" + str(uuid.uuid4())
        _seed_agent(test_db)
        _seed_run(test_db, run_id, state="promoted")
        test_db.commit()

        with pytest.raises(dbmod.IllegalTransition):
            dbmod.transition(
                test_db, run_id,
                expected_from="",
                to="published",  # not legal from promoted
                actor_type="worker",
                actor_id="test",
                reason="test",
            )
        test_db.rollback()

    def test_transition_run_not_found(self, test_db):
        with pytest.raises(dbmod.RunNotFound):
            dbmod.transition(
                test_db, "nonexistent-run-id",
                expected_from="", to="parsing",
                actor_type="worker", actor_id="test", reason="test",
            )
        test_db.rollback()

    def test_claim_returns_none_when_empty(self, test_db):
        job = dbmod.claim_job(test_db, "worker-x", ["parse_run"])
        assert job is None

    def test_fail_job_exponential_backoff(self, test_db):
        run_id = "run-" + str(uuid.uuid4())
        _seed_agent(test_db)
        _seed_run(test_db, run_id, state="promoted")
        job_id = _seed_job(test_db, run_id)
        test_db.commit()

        job = dbmod.claim_job(test_db, "worker-x", ["parse_run"])
        assert job is not None

        dead = dbmod.fail_job(test_db, job_id, "test error")
        test_db.commit()
        assert not dead

        row = test_db.execute(
            "SELECT state FROM jobs WHERE id = %s", (job_id,)
        ).fetchone()
        assert row["state"] == "queued"
