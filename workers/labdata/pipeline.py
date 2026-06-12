"""
labdata.pipeline — parse_run job handler (Pipeline-A, architecture §17).

Flow per job:
  1. Load run row + manifest (latest by created_at) + raw-file directory.
  2. Transition: current → 'parsing'  (tolerates 'promoted' or 'needs_metadata').
  3. Route by measurement family:
       - VAC/IV/breakdown  → notebook iv_breakdown (papermill)
       - cv/cf/impedance   → notebook impedance_cv_cf (papermill)
       - fet_transfer       → existing Python path (UNCHANGED)
  4. On success:
       a. INSERT analysis_artifacts rows (plots + notebook).
       b. INSERT review_artifacts row.
       c. Transition parsing → validated → processing → awaiting_review.
       d. Insert notification + enqueue send_notification job.
       e. Complete the job.

State transitions use transition_run() exclusively (never direct UPDATE).
"""

from __future__ import annotations

import hashlib
import json
import os
import traceback
from pathlib import Path
from typing import Any

import matplotlib
matplotlib.use("Agg")   # non-interactive backend; no display required
import matplotlib.pyplot as plt
import numpy as np
import psycopg

from labdata import db as dbmod
from labdata.parsers import fet_transfer

WORKER_ACTOR_TYPE = "worker"
PARSER_VERSION = fet_transfer.PARSER_VERSION
ANALYSIS_VERSION = 1

# Path to the notebooks directory (sibling of labdata/)
_NOTEBOOKS_DIR = Path(__file__).parent.parent / "notebooks"


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def handle_parse_run(
    conn: psycopg.Connection,
    job: dict,
    labdata_root: str,
    worker_id: str = "pipeline-a",
) -> dict:
    """
    Process a single parse_run job.  Returns a result dict suitable for
    complete_job().  Raises on unexpected/unrecoverable errors.
    """
    run_id = job["run_id"]
    if not run_id:
        raise ValueError(f"job {job['id']} has no run_id")

    # ------------------------------------------------------------------
    # 1. Load run + manifest
    # ------------------------------------------------------------------
    run = _load_run(conn, run_id)
    manifest = _load_manifest(conn, run_id)
    manifest_json = manifest["raw_json"]

    measurement_type = manifest_json.get("measurement_type") or run["measurement_type"] or ""
    sample_id = manifest_json.get("sample_id") or run["sample_id"] or run_id
    device_id = manifest_json.get("device_id") or run["device_id"] or "unknown"
    files_list = manifest_json.get("files", [])

    raw_dir = Path(labdata_root) / "raw" / sample_id / device_id / run_id

    # ------------------------------------------------------------------
    # 2. Transition current_state → 'parsing'
    # ------------------------------------------------------------------
    _transition(conn, run_id, "", "parsing", worker_id,
                "pipeline-a: starting parse_run job")

    # ------------------------------------------------------------------
    # 3. Route by measurement family
    # ------------------------------------------------------------------
    family = _detect_family(measurement_type, files_list)

    if family in ("iv_breakdown", "impedance_cv_cf"):
        return _handle_notebook_family(
            conn, job, labdata_root, worker_id,
            run_id, sample_id, device_id, measurement_type, family, raw_dir,
        )
    else:
        return _handle_fet_transfer(
            conn, job, labdata_root, worker_id,
            run_id, sample_id, device_id, measurement_type, files_list, raw_dir,
        )


# ---------------------------------------------------------------------------
# Family detection
# ---------------------------------------------------------------------------

def _detect_family(measurement_type: str, files_list: list) -> str:
    """
    Return the processing family name based on measurement_type and files.

      'iv_breakdown'     — measurement_type contains VAC / IV / breakdown
      'impedance_cv_cf'  — measurement_type or filenames contain cv/cf/impedance
      'fet_transfer'     — everything else
    """
    mt_lower = measurement_type.lower()

    if any(kw in mt_lower for kw in ("vac", "iv", "breakdown")):
        return "iv_breakdown"

    # Check for cv/cf/impedance in measurement_type or file names
    file_names = " ".join(
        (f.get("name", "") if isinstance(f, dict) else str(f)).lower()
        for f in files_list
    )
    if any(kw in mt_lower for kw in ("cv", "cf", "impedance")):
        return "impedance_cv_cf"
    if any(kw in file_names for kw in ("_cv_", "_cf_", "impedance")):
        return "impedance_cv_cf"

    return "fet_transfer"


# ---------------------------------------------------------------------------
# Notebook-based handler (iv_breakdown and impedance_cv_cf)
# ---------------------------------------------------------------------------

def _handle_notebook_family(
    conn: psycopg.Connection,
    job: dict,
    labdata_root: str,
    worker_id: str,
    run_id: str,
    sample_id: str,
    device_id: str,
    measurement_type: str,
    family: str,
    raw_dir: Path,
) -> dict:
    """
    Record a device-level parse result and transition parsing → validated.

    For CV/CF and VAC/IV device runs the actual analysis now happens at the
    session level via handle_process_session.  This handler records the device
    data is present (parser_results status='ok') and stops at 'validated'.
    Review and notification are deferred to the session processor.
    """
    # Record that the device data was ingested
    _insert_notebook_parser_results(conn, run_id, family, {})

    # Transition: parsing → validated (stop here; session processor handles the rest)
    _transition(conn, run_id, "parsing", "validated", worker_id,
                f"pipeline-a: device data recorded ({family}); awaiting session review")

    return {
        "status": "validated",
        "run_id": run_id,
        "family": family,
    }


# ---------------------------------------------------------------------------
# FET transfer handler (UNCHANGED logic)
# ---------------------------------------------------------------------------

def _handle_fet_transfer(
    conn: psycopg.Connection,
    job: dict,
    labdata_root: str,
    worker_id: str,
    run_id: str,
    sample_id: str,
    device_id: str,
    measurement_type: str,
    files_list: list,
    raw_dir: Path,
) -> dict:
    """Original Pipeline-A handler for FET transfer curves."""

    # ------------------------------------------------------------------
    # 3. Parse the primary data file
    # ------------------------------------------------------------------
    try:
        primary_file = _pick_primary_file(files_list, measurement_type)
        data_path = raw_dir / primary_file
        text = data_path.read_text(errors="replace")
        parse_result = fet_transfer.parse(text)
    except Exception as exc:
        _handle_parse_failure(conn, run_id, worker_id, str(exc), None, None)
        return {"status": "parser_failed", "error": str(exc)}

    # ------------------------------------------------------------------
    # 4. Validation failure
    # ------------------------------------------------------------------
    if not parse_result.ok:
        _handle_parse_failure(
            conn, run_id, worker_id,
            parse_result.error or "parse failed",
            parse_result, None,
        )
        return {"status": "parser_failed", "error": parse_result.error}

    # ------------------------------------------------------------------
    # 5a. Compute metrics
    # ------------------------------------------------------------------
    try:
        metrics = fet_transfer.compute_metrics(parse_result)
    except Exception as exc:
        _handle_parse_failure(conn, run_id, worker_id,
                               f"metrics computation failed: {exc}",
                               parse_result, None)
        return {"status": "parser_failed", "error": str(exc)}

    # ------------------------------------------------------------------
    # 5b. Render I-V plot PNG
    # ------------------------------------------------------------------
    out_dir = Path(labdata_root) / "processed" / run_id / f"analysis_version_{ANALYSIS_VERSION:03d}"
    out_dir.mkdir(parents=True, exist_ok=True)
    plot_path = out_dir / "transfer.png"

    _render_plot(parse_result.voltage, parse_result.current,
                 plot_path, run_id, metrics)

    plot_sha256 = _sha256_file(plot_path)

    # ------------------------------------------------------------------
    # 5c-e. DB inserts (parser_results, analysis_artifacts, review_artifacts)
    # ------------------------------------------------------------------
    metrics_dict = {
        "I_max": metrics.I_max,
        "I_min": metrics.I_min,
        "on_off_ratio": (
            metrics.on_off_ratio
            if not (isinstance(metrics.on_off_ratio, float) and
                    (metrics.on_off_ratio != metrics.on_off_ratio or
                     metrics.on_off_ratio == float("inf")))
            else str(metrics.on_off_ratio)
        ),
        "V_th": metrics.V_th,
    }

    _insert_parser_results(
        conn, run_id, parse_result, metrics_dict,
    )
    _insert_analysis_artifact(
        conn, run_id, ANALYSIS_VERSION, "plot", str(plot_path), plot_sha256,
    )
    _insert_review_artifacts(
        conn, run_id,
        metrics_json=metrics_dict,
        plots_json=[str(plot_path)],
        parser_warnings_json=parse_result.warnings,
        llm_summary="",
    )

    # ------------------------------------------------------------------
    # 5f. State transitions: parsing → validated → processing → awaiting_review
    # ------------------------------------------------------------------
    _transition(conn, run_id, "parsing", "validated", worker_id,
                "pipeline-a: parse succeeded")
    _transition(conn, run_id, "validated", "processing", worker_id,
                "pipeline-a: computing metrics")
    _transition(conn, run_id, "processing", "awaiting_review", worker_id,
                "pipeline-a: review artifacts written")

    return {
        "status": "awaiting_review",
        "metrics": metrics_dict,
        "plot": str(plot_path),
        "plot_sha256": plot_sha256,
        "rows_actual": parse_result.rows_actual,
        "warnings": parse_result.warnings,
    }


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _load_run(conn: psycopg.Connection, run_id: str) -> dict:
    with conn.cursor() as cur:
        cur.execute(
            "SELECT id, state, measurement_type, sample_id, device_id "
            "FROM runs WHERE id = %s",
            (run_id,),
        )
        row = cur.fetchone()
    if row is None:
        raise ValueError(f"run {run_id} not found")
    return row


def _load_manifest(conn: psycopg.Connection, run_id: str) -> dict:
    with conn.cursor() as cur:
        cur.execute(
            "SELECT raw_json FROM manifests WHERE run_id = %s "
            "ORDER BY created_at DESC LIMIT 1",
            (run_id,),
        )
        row = cur.fetchone()
    if row is None:
        raise ValueError(f"no manifest found for run {run_id}")
    return row


def _pick_primary_file(files_list: list[dict], measurement_type: str) -> str:
    """
    Choose the primary data file from the manifest files list.

    Priority:
      1. A file whose name ends in '.data' or '.dat' or '.csv' or '.txt'
         and whose name does not contain 'params' or 'json'.
      2. The first non-JSON file.
      3. The very first file.
    """
    if not files_list:
        raise ValueError("manifest has no files listed")

    data_exts = {".data", ".dat", ".csv", ".txt"}
    candidates = []
    for f in files_list:
        name = f.get("name", "") if isinstance(f, dict) else str(f)
        ext = Path(name).suffix.lower()
        if ext in data_exts and "params" not in name.lower():
            candidates.append(name)

    if candidates:
        return candidates[0]

    # Fallback: first non-JSON file
    for f in files_list:
        name = f.get("name", "") if isinstance(f, dict) else str(f)
        if not name.lower().endswith(".json"):
            return name

    # Last resort
    f = files_list[0]
    return f.get("name", "") if isinstance(f, dict) else str(f)


def _transition(
    conn: psycopg.Connection,
    run_id: str,
    expected_from: str,
    to: str,
    worker_id: str,
    reason: str,
) -> None:
    dbmod.transition(
        conn, run_id, expected_from, to,
        WORKER_ACTOR_TYPE, worker_id, reason,
    )


def _handle_parse_failure(
    conn: psycopg.Connection,
    run_id: str,
    worker_id: str,
    error_msg: str,
    parse_result,
    metrics_dict,
) -> None:
    """Transition to parser_failed and record a minimal parser_results row."""
    warnings = parse_result.warnings if parse_result else []
    try:
        _insert_parser_results_failure(conn, run_id, error_msg, warnings)
    except Exception:
        pass  # best effort

    try:
        _transition(conn, run_id, "parsing", "parser_failed", worker_id,
                    f"pipeline-a: parse failed: {error_msg}")
    except Exception:
        # If transition fails (e.g. already in wrong state), don't mask original error.
        pass


def _sanitize_metrics_for_json(d: dict) -> dict:
    """
    Replace float NaN / Inf with None so json.dumps() produces valid JSON
    that PostgreSQL accepts as jsonb.
    """
    import math

    def _clean(v):
        if isinstance(v, float) and (math.isnan(v) or math.isinf(v)):
            return None
        if isinstance(v, dict):
            return {k: _clean(vv) for k, vv in v.items()}
        if isinstance(v, list):
            return [_clean(vv) for vv in v]
        return v

    return {k: _clean(v) for k, v in d.items()}


def _insert_notebook_parser_results(
    conn: psycopg.Connection,
    run_id: str,
    family: str,
    metrics_dict: dict,
) -> None:
    """Insert a parser_results row for notebook-executed families."""
    safe_metrics = _sanitize_metrics_for_json(metrics_dict)
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO parser_results
                (run_id, parser_version, status, columns_expected,
                 rows_declared, rows_actual, warnings_json, output_json)
            VALUES (%s, %s, %s, %s, %s, %s, %s::jsonb, %s::jsonb)
            """,
            (
                run_id,
                f"notebook_{family}_v1.0",
                "ok",
                0,           # not applicable for notebooks
                None,
                0,
                json.dumps([]),
                json.dumps(safe_metrics),
            ),
        )


def _insert_parser_results(
    conn: psycopg.Connection,
    run_id: str,
    parse_result,
    metrics_dict: dict,
) -> None:
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO parser_results
                (run_id, parser_version, status, columns_expected,
                 rows_declared, rows_actual, warnings_json, output_json)
            VALUES (%s, %s, %s, %s, %s, %s, %s::jsonb, %s::jsonb)
            """,
            (
                run_id,
                PARSER_VERSION,
                "ok",
                fet_transfer.COLUMNS_EXPECTED,
                parse_result.rows_declared,
                parse_result.rows_actual,
                json.dumps(parse_result.warnings),
                json.dumps(metrics_dict),
            ),
        )


def _insert_parser_results_failure(
    conn: psycopg.Connection,
    run_id: str,
    error_msg: str,
    warnings: list[str],
) -> None:
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO parser_results
                (run_id, parser_version, status, columns_expected,
                 warnings_json, output_json)
            VALUES (%s, %s, %s, %s, %s::jsonb, %s::jsonb)
            """,
            (
                run_id,
                PARSER_VERSION,
                "failed",
                fet_transfer.COLUMNS_EXPECTED,
                json.dumps(warnings),
                json.dumps({"error": error_msg}),
            ),
        )


def _insert_analysis_artifact(
    conn: psycopg.Connection,
    run_id: str,
    analysis_version: int,
    kind: str,
    path: str,
    sha256: str,
) -> None:
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO analysis_artifacts
                (run_id, analysis_version, kind, path, sha256)
            VALUES (%s, %s, %s, %s, %s)
            """,
            (run_id, analysis_version, kind, path, sha256),
        )


def _insert_review_artifacts(
    conn: psycopg.Connection,
    run_id: str,
    metrics_json: dict,
    plots_json: list[str],
    parser_warnings_json: list[str],
    llm_summary: str,
) -> None:
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO review_artifacts
                (run_id, version, metrics_json, plots_json,
                 parser_warnings_json, llm_summary)
            VALUES (%s, %s, %s::jsonb, %s::jsonb, %s::jsonb, %s)
            """,
            (
                run_id,
                ANALYSIS_VERSION,
                json.dumps(metrics_json),
                json.dumps(plots_json),
                json.dumps(parser_warnings_json),
                llm_summary,
            ),
        )


def _render_plot(
    V: np.ndarray,
    I: np.ndarray,
    path: Path,
    run_id: str,
    metrics,
) -> None:
    """Render an I-V transfer curve to PNG using the Agg backend."""
    fig, ax = plt.subplots(figsize=(6, 4))
    ax.plot(V, np.abs(I), "b-o", markersize=3, linewidth=1.2, label="|I_D|")
    ax.set_xlabel("Gate Voltage V_G (V)")
    ax.set_ylabel("Drain Current |I_D| (A)")
    ax.set_title(f"FET Transfer Curve\nrun {run_id[:16]}{'…' if len(run_id) > 16 else ''}")
    ax.set_yscale("log") if np.any(np.abs(I) > 0) else None

    if metrics.V_th is not None:
        ax.axvline(metrics.V_th, color="r", linestyle="--",
                   alpha=0.7, label=f"V_th ≈ {metrics.V_th:.2f} V")

    on_off_str = (
        f"{metrics.on_off_ratio:.1e}"
        if isinstance(metrics.on_off_ratio, float)
        and not (metrics.on_off_ratio != metrics.on_off_ratio)
        and metrics.on_off_ratio != float("inf")
        else str(metrics.on_off_ratio)
    )
    ax.legend(title=f"on/off ≈ {on_off_str}", fontsize=8)
    ax.grid(True, which="both", alpha=0.3)

    fig.tight_layout()
    fig.savefig(str(path), dpi=120, format="png")
    plt.close(fig)


def _sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()
