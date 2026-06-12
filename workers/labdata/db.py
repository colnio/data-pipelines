"""
labdata.db — thin Postgres helpers for the Pipeline-A worker.

Connection is created from DATABASE_URL (default:
postgres://lab:lab@localhost:5432/labdata?sslmode=disable).

Job-queue semantics mirror internal/jobs/queue.go:
  claim_job   — SELECT ... FOR UPDATE SKIP LOCKED, then UPDATE to running
  complete_job — UPDATE state='succeeded', store result
  fail_job    — exponential backoff or 'dead' when max_attempts exhausted
  transition  — calls SELECT transition_run(...) and maps RAISE codes to Python
"""

from __future__ import annotations

import json
import math
import os
import time
from typing import Any

import psycopg
from psycopg.rows import dict_row

# ---------------------------------------------------------------------------
# Exception hierarchy
# ---------------------------------------------------------------------------

class TransitionError(Exception):
    """Base for transition_run errors returned from Postgres."""

class RunNotFound(TransitionError):
    pass

class StateMismatch(TransitionError):
    pass

class IllegalTransition(TransitionError):
    pass

# ---------------------------------------------------------------------------
# Connection helpers
# ---------------------------------------------------------------------------

_DEFAULT_URL = "postgres://lab:lab@localhost:5432/labdata?sslmode=disable"


def connect(url: str | None = None) -> psycopg.Connection:
    """Open a new psycopg connection from *url* (or DATABASE_URL env var)."""
    dsn = url or os.environ.get("DATABASE_URL", _DEFAULT_URL)
    return psycopg.connect(dsn, row_factory=dict_row)


# ---------------------------------------------------------------------------
# Job queue
# ---------------------------------------------------------------------------

_LOCK_TTL_SECONDS = 300  # 5 minutes; worker must renew or crash before then


def claim_job(
    conn: psycopg.Connection,
    worker_id: str,
    job_types: list[str],
    lock_ttl_seconds: int = _LOCK_TTL_SECONDS,
) -> dict | None:
    """
    Atomically claim the highest-priority oldest available job of the given
    types.  Returns the job dict or None if no job is available.

    Mirrors ClaimTypes in internal/jobs/queue.go:
      - WHERE state='queued' AND available_at<=now() AND job_type=ANY(...)
      - ORDER BY priority DESC, created_at ASC
      - FOR UPDATE SKIP LOCKED
      - UPDATE state='running', locked_by, locked_until, started_at,
               attempt_count+1
    """
    with conn.transaction():
        with conn.cursor() as cur:
            if job_types:
                cur.execute(
                    """
                    SELECT id FROM jobs
                    WHERE state = 'queued'
                      AND available_at <= now()
                      AND job_type = ANY(%s)
                    ORDER BY priority DESC, created_at ASC
                    FOR UPDATE SKIP LOCKED
                    LIMIT 1
                    """,
                    (job_types,),
                )
            else:
                cur.execute(
                    """
                    SELECT id FROM jobs
                    WHERE state = 'queued'
                      AND available_at <= now()
                    ORDER BY priority DESC, created_at ASC
                    FOR UPDATE SKIP LOCKED
                    LIMIT 1
                    """,
                )
            row = cur.fetchone()
            if row is None:
                return None

            job_id = row["id"]
            cur.execute(
                """
                UPDATE jobs
                SET state         = 'running',
                    locked_by     = %s,
                    locked_until  = now() + (%s || ' seconds')::interval,
                    started_at    = COALESCE(started_at, now()),
                    attempt_count = attempt_count + 1
                WHERE id = %s
                RETURNING id, job_type, run_id, state, priority,
                          attempt_count, max_attempts,
                          available_at, locked_by, locked_until,
                          idempotency_key, payload_json, result_json,
                          last_error, created_at, started_at, finished_at
                """,
                (worker_id, str(lock_ttl_seconds), job_id),
            )
            return cur.fetchone()


def complete_job(
    conn: psycopg.Connection,
    job_id: int,
    result: dict | None = None,
) -> None:
    """Mark a job succeeded and store its result JSON."""
    result_json = json.dumps(result or {})
    with conn.cursor() as cur:
        cur.execute(
            """
            UPDATE jobs
            SET state        = 'succeeded',
                result_json  = %s,
                finished_at  = now(),
                locked_by    = NULL,
                locked_until = NULL
            WHERE id = %s
            """,
            (result_json, job_id),
        )
        if cur.rowcount == 0:
            raise ValueError(f"complete_job: job {job_id} not found")


def fail_job(
    conn: psycopg.Connection,
    job_id: int,
    err_msg: str,
    max_attempts: int | None = None,
) -> bool:
    """
    Record a job failure.  Returns True if the job is now 'dead'.

    If attempt_count >= max_attempts the job is set to 'dead'.
    Otherwise it is re-queued with exponential backoff (2^attempt seconds,
    capped at 300s), mirroring backoff() in internal/jobs/queue.go.
    """
    with conn.cursor() as cur:
        cur.execute(
            "SELECT attempt_count, max_attempts FROM jobs WHERE id = %s",
            (job_id,),
        )
        row = cur.fetchone()
        if row is None:
            raise ValueError(f"fail_job: job {job_id} not found")
        attempts = row["attempt_count"]
        effective_max = max_attempts if max_attempts is not None else row["max_attempts"]

    if attempts >= effective_max:
        with conn.cursor() as cur:
            cur.execute(
                """
                UPDATE jobs
                SET state        = 'dead',
                    last_error   = %s,
                    finished_at  = now(),
                    locked_by    = NULL,
                    locked_until = NULL
                WHERE id = %s
                """,
                (err_msg, job_id),
            )
        return True

    # Exponential backoff capped at 5 min, same as Go backoff().
    secs = min(int(math.pow(2, attempts)), 300)
    with conn.cursor() as cur:
        cur.execute(
            """
            UPDATE jobs
            SET state        = 'queued',
                available_at = now() + (%s || ' seconds')::interval,
                last_error   = %s,
                locked_by    = NULL,
                locked_until = NULL
            WHERE id = %s
            """,
            (str(secs), err_msg, job_id),
        )
    return False


# ---------------------------------------------------------------------------
# State transitions
# ---------------------------------------------------------------------------

def transition(
    conn: psycopg.Connection,
    run_id: str,
    expected_from: str,
    to: str,
    actor_type: str,
    actor_id: str,
    reason: str,
    payload: dict | None = None,
) -> int:
    """
    Call transition_run() and return the run_state_transitions row id.

    Maps Postgres RAISE error messages to Python exceptions:
      'run_not_found'      → RunNotFound
      'state_mismatch'     → StateMismatch
      'illegal_transition' → IllegalTransition
    """
    payload_json = json.dumps(payload or {})
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT transition_run(%s, %s, %s, %s, %s, %s, %s::jsonb)",
                (run_id, expected_from, to, actor_type, actor_id, reason, payload_json),
            )
            row = cur.fetchone()
            # row is a dict because conn uses dict_row; value is first column
            val = next(iter(row.values()))
            return int(val)
    except Exception as exc:
        # transition_run uses ERRCODE='P0002' (NoDataFound) for run_not_found
        # and ERRCODE='P0001' (RaiseException) for state_mismatch / illegal_transition.
        # psycopg maps these to NoDataFound and RaiseException respectively.
        msg = str(exc)
        if "run_not_found" in msg:
            raise RunNotFound(msg) from exc
        if "state_mismatch" in msg:
            raise StateMismatch(msg) from exc
        if "illegal_transition" in msg:
            raise IllegalTransition(msg) from exc
        raise


# ---------------------------------------------------------------------------
# Processing parameters
# ---------------------------------------------------------------------------

def load_processing_params(conn: psycopg.Connection, sample_id: str) -> dict | None:
    """
    Return the latest processing_parameter_versions row for this sample, or None.

    The row is expected to have params_json shaped:
      {"thickness_nm": 2.5, "area_um2_by_size": {"tiny": 6333, ...}}
    """
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT params_json FROM processing_parameter_versions
            WHERE scope = 'sample' AND scope_key = %s
            ORDER BY version DESC
            LIMIT 1
            """,
            (sample_id,),
        )
        row = cur.fetchone()
    if row is None:
        return None
    return row["params_json"]


# ---------------------------------------------------------------------------
# Notification + job-queue helpers
# ---------------------------------------------------------------------------

def insert_notification(
    conn: psycopg.Connection,
    event_type: str,
    run_id: str,
    payload: dict,
) -> int:
    """
    Insert a pending notification row.  Returns the new notification id.

    Shape mirrors Go notify package:
      INSERT INTO notifications (event_type, channel, target, payload_json, status)
      VALUES (%s, 'telegram', '', %s::jsonb, 'pending')
    """
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO notifications
                (event_type, channel, target, payload_json, status)
            VALUES (%s, 'telegram', '', %s::jsonb, 'pending')
            RETURNING id
            """,
            (event_type, json.dumps(payload)),
        )
        row = cur.fetchone()
    return int(row["id"])


def enqueue_job(
    conn: psycopg.Connection,
    job_type: str,
    run_id: str,
    idempotency_key: str,
    payload: dict,
) -> None:
    """
    Enqueue a job with ON CONFLICT DO NOTHING on idempotency_key.

    Mirrors Go internal/jobs/queue.go Enqueue():
      priority=3, max_attempts=5, available_at=now()
    """
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO jobs
                (job_type, run_id, priority, max_attempts,
                 available_at, idempotency_key, payload_json)
            VALUES (%s, %s, 3, 5, now(), %s, %s::jsonb)
            ON CONFLICT (idempotency_key) DO NOTHING
            """,
            (job_type, run_id, idempotency_key, json.dumps(payload)),
        )
