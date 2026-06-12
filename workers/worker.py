#!/usr/bin/env python3
"""
worker.py — Pipeline-A worker entrypoint.

Polls the jobs table for parse_run jobs, dispatches to
labdata.pipeline.handle_parse_run, and marks jobs succeeded or failed.

Environment variables:
  DATABASE_URL   Postgres DSN (default: postgres://lab:lab@localhost:5432/labdata?sslmode=disable)
  LABDATA_ROOT   Root of the lab data tree (default: /srv/labdata)
  WORKER_ID      Unique name for this worker process (default: pipeline-a-<pid>)

Ctrl-C / SIGINT causes a clean exit after the current job finishes.
"""

from __future__ import annotations

import os
import signal
import sys
import time
import traceback
import uuid

# Ensure the package root is importable when run as a script
sys.path.insert(0, os.path.dirname(__file__))

from labdata import db as dbmod
from labdata.pipeline import handle_parse_run
from labdata.process_session import handle_process_session

POLL_INTERVAL = 2.0   # seconds between polls when queue is empty
JOB_TYPES = ["parse_run", "process_session"]


def main() -> None:
    database_url = os.environ.get("DATABASE_URL",
                                  "postgres://lab:lab@localhost:5432/labdata?sslmode=disable")
    labdata_root = os.environ.get("LABDATA_ROOT", "/srv/labdata")
    worker_id = os.environ.get("WORKER_ID", f"pipeline-a-{os.getpid()}")

    # Graceful shutdown on SIGINT
    running = [True]

    def _sigint(signum, frame):
        print(f"\n[worker] {worker_id}: SIGINT received, shutting down after current job …",
              flush=True)
        running[0] = False

    signal.signal(signal.SIGINT, _sigint)

    print(f"[worker] {worker_id} starting. DB={database_url}  ROOT={labdata_root}",
          flush=True)

    conn = dbmod.connect(database_url)
    conn.autocommit = True   # each helper manages its own transaction

    try:
        while running[0]:
            try:
                job = dbmod.claim_job(conn, worker_id, JOB_TYPES)
            except Exception as exc:
                print(f"[worker] claim error: {exc}", flush=True)
                time.sleep(POLL_INTERVAL)
                continue

            if job is None:
                time.sleep(POLL_INTERVAL)
                continue

            job_id = job["id"]
            print(f"[worker] claimed job {job_id} type={job['job_type']} "
                  f"run={job['run_id']}", flush=True)

            try:
                job_type = job["job_type"]
                if job_type == "parse_run":
                    result = handle_parse_run(conn, job, labdata_root, worker_id)
                elif job_type == "process_session":
                    result = handle_process_session(conn, job, labdata_root, worker_id)
                else:
                    raise ValueError(f"unknown job_type: {job_type!r}")
                dbmod.complete_job(conn, job_id, result)
                print(f"[worker] job {job_id} succeeded: {result.get('status')}", flush=True)
            except Exception as exc:
                err_msg = traceback.format_exc()
                print(f"[worker] job {job_id} failed: {exc}", flush=True)
                try:
                    dead = dbmod.fail_job(conn, job_id, str(exc))
                    if dead:
                        print(f"[worker] job {job_id} is now dead (exhausted retries)",
                              flush=True)
                except Exception as fail_exc:
                    print(f"[worker] fail_job error: {fail_exc}", flush=True)

    finally:
        conn.close()
        print(f"[worker] {worker_id} exited.", flush=True)


if __name__ == "__main__":
    main()
