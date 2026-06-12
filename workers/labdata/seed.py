"""
labdata.seed — dev/test seeding helpers for JupyterHub notebooks (architecture §18).

seed_demo() inserts a minimal but complete pipeline run into the database and
writes small representative files to disk, so notebooks can be developed and
tested without a real instrument.

The write connection uses DATABASE_URL (env) or the supplied write_url.
Files are written under LABDATA_ROOT (env) or the supplied labdata_root.
"""

from __future__ import annotations

import hashlib
import json
import os
import textwrap
from pathlib import Path
from typing import Optional

import psycopg
from psycopg.rows import dict_row

_DEFAULT_DB = "postgres://lab:lab@localhost:5432/labdata?sslmode=disable"
_DEFAULT_ROOT = "/srv/labdata"

# Small valid FET transfer CSV used as the seeded raw data file
_DEMO_CSV = textwrap.dedent("""\
    # FET transfer sweep — demo seed
    # nrows=5
    V,I
    -1.0,1.0e-12
     0.0,5.0e-11
     1.0,2.5e-9
     2.0,1.0e-7
     3.0,4.0e-6
""")

# Tiny PNG (1×1 white pixel) to stand in for a real plot
_TINY_PNG = (
    b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"
    b"\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00"
    b"\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82"
)


def _sanitize(s: Optional[str]) -> str:
    """Mirror Go sanitizeSeg."""
    if not s:
        return "_"
    s = s.replace("/", "_").replace("\\", "_").strip()
    if s == "" or s == "." or s == "..":
        return "_"
    return s


def _sanitize_or_unknown(s: Optional[str]) -> str:
    if not s:
        return "_unknown"
    cleaned = _sanitize(s)
    return "_unknown" if cleaned == "_" else cleaned


def seed_demo(
    write_url: Optional[str] = None,
    labdata_root: Optional[str] = None,
) -> dict:
    """Insert a full demo pipeline run and write seed files.

    The demo run is driven to 'published' state with:
      - sample DEMO1 / device demo_dev_1 (device_class 'fet')
      - run_id demo-run-0001
      - one run_file: iv_sweep.data
      - a review_artifacts row with on_off_ratio metrics
      - a published_results + published_artifacts row

    Files written
    -------------
    raw/DEMO1/demo_dev_1/demo-run-0001/iv_sweep.data  (small CSV)
    processed/demo-run-0001/analysis_version_001/transfer.png  (tiny PNG)

    Returns
    -------
    dict with keys: run_id, sample_id, device_id, raw_dir, published_result_id
    """
    dsn = write_url or os.environ.get("DATABASE_URL") or _DEFAULT_DB
    root = Path(labdata_root or os.environ.get("LABDATA_ROOT") or _DEFAULT_ROOT)

    sample_id = "DEMO1"
    device_id = "demo_dev_1"
    run_id = "demo-run-0001"
    agent_id = "demo-seed-agent"

    # ---- Write files first (files first, DB last — architecture golden rule #3)
    raw_path = (
        root / "raw"
        / _sanitize_or_unknown(sample_id)
        / _sanitize_or_unknown(device_id)
        / _sanitize(run_id)
    )
    raw_path.mkdir(parents=True, exist_ok=True)
    data_file = raw_path / "iv_sweep.data"
    data_bytes = _DEMO_CSV.encode()
    data_file.write_bytes(data_bytes)
    data_sha = hashlib.sha256(data_bytes).hexdigest()

    proc_path = root / "processed" / _sanitize(run_id) / "analysis_version_001"
    proc_path.mkdir(parents=True, exist_ok=True)
    plot_file = proc_path / "transfer.png"
    plot_file.write_bytes(_TINY_PNG)
    plot_sha = hashlib.sha256(_TINY_PNG).hexdigest()

    # ---- DB inserts
    conn = psycopg.connect(dsn, row_factory=dict_row)
    try:
        with conn.transaction():
            # Agent (idempotent)
            conn.execute(
                """
                INSERT INTO agents (id, display_name, key_hash)
                VALUES (%s, %s, %s)
                ON CONFLICT (id) DO NOTHING
                """,
                (agent_id, "Demo Seed Agent", "$2b$10$demodemodemodemodemodem"),
            )

            # Sample (idempotent)
            conn.execute(
                """
                INSERT INTO samples (id, material_stack, notes)
                VALUES (%s, %s, %s)
                ON CONFLICT (id) DO NOTHING
                """,
                (sample_id, "graphene/SiO2", "Demo sample for notebook tests"),
            )

            # Device (idempotent)
            conn.execute(
                """
                INSERT INTO devices (id, sample_id, device_class, notes)
                VALUES (%s, %s, %s, %s)
                ON CONFLICT (id) DO NOTHING
                """,
                (device_id, sample_id, "fet", "Demo FET device"),
            )

            # Run — delete old then re-insert so seed is repeatable.
            # Must delete child rows in dependency order since published_results
            # has a non-CASCADE FK to runs.
            conn.execute(
                "DELETE FROM published_artifacts WHERE published_result_id IN "
                "(SELECT id FROM published_results WHERE run_id = %s)",
                (run_id,),
            )
            conn.execute(
                "DELETE FROM published_results WHERE run_id = %s", (run_id,)
            )
            conn.execute("DELETE FROM runs WHERE id = %s", (run_id,))
            conn.execute(
                """
                INSERT INTO runs
                    (id, manifest_hash, agent_id, measurement_type, completion_source,
                     sample_id, device_id, meas_path, state, declared_at)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, now())
                """,
                (
                    run_id,
                    "demo-hash-0001",
                    agent_id,
                    "fet_transfer",
                    "operator",
                    sample_id,
                    device_id,
                    f"/data/runs/{sample_id}/{device_id}/{run_id}",
                    "published",
                ),
            )

            # run_files
            conn.execute(
                """
                INSERT INTO run_files (run_id, name, bytes, sha256)
                VALUES (%s, %s, %s, %s)
                ON CONFLICT (run_id, name) DO NOTHING
                """,
                (run_id, "iv_sweep.data", len(data_bytes), data_sha),
            )

            # review_artifacts
            metrics = {
                "I_max": 4.0e-6,
                "I_min": 1.0e-12,
                "on_off_ratio": 4000000.0,
                "V_th": 0.5,
            }
            conn.execute(
                """
                INSERT INTO review_artifacts
                    (run_id, version, summary_json, metrics_json, plots_json, llm_summary)
                VALUES (%s, %s, %s::jsonb, %s::jsonb, %s::jsonb, %s)
                """,
                (
                    run_id,
                    1,
                    json.dumps({"note": "demo"}),
                    json.dumps(metrics),
                    json.dumps([str(plot_file)]),
                    "Demo run — auto seeded",
                ),
            )

            # review_decision (needed for published_results FK)
            ra_id = conn.execute(
                "SELECT id FROM review_artifacts WHERE run_id = %s ORDER BY id DESC LIMIT 1",
                (run_id,),
            ).fetchone()["id"]

            rd_row = conn.execute(
                """
                INSERT INTO review_decisions
                    (run_id, review_artifact_id, decision, reviewer, reason)
                VALUES (%s, %s, %s, %s, %s)
                RETURNING id
                """,
                (run_id, ra_id, "approve", "demo-seed", "seeded by seed_demo"),
            ).fetchone()
            rd_id = rd_row["id"]

            # published_results — raw_file_hashes_json matches the Go publisher's
            # shape exactly: a list of {name, sha256} from run_files (the §5
            # reproducibility receipt that notebooks reconstruct against).
            raw_file_hashes = [{"name": "iv_sweep.data", "sha256": data_sha}]
            pub_row = conn.execute(
                """
                INSERT INTO published_results
                    (run_id, manifest_hash, raw_file_hashes_json, review_decision_id, published_by)
                VALUES (%s, %s, %s::jsonb, %s, %s)
                RETURNING id
                """,
                (run_id, "demo-hash-0001", json.dumps(raw_file_hashes), rd_id, "demo-seed"),
            ).fetchone()
            published_result_id = str(pub_row["id"])

            # published_artifacts
            conn.execute(
                """
                INSERT INTO published_artifacts
                    (published_result_id, path, sha256, kind, bytes)
                VALUES (%s, %s, %s, %s, %s)
                """,
                (
                    published_result_id,
                    str(plot_file),
                    plot_sha,
                    "plot",
                    len(_TINY_PNG),
                ),
            )

    finally:
        conn.close()

    return {
        "run_id": run_id,
        "sample_id": sample_id,
        "device_id": device_id,
        "raw_dir": str(raw_path),
        "published_result_id": published_result_id,
    }
