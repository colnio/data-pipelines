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
    """Execute the appropriate notebook, insert artifacts, transition states."""
    from labdata.notebook_runner import run_processing
    from labdata.seed import area_um2_for_device, _DEFAULT_THICKNESS_NM, _DEFAULT_AREA_UM2_BY_SIZE

    out_dir = (
        Path(labdata_root)
        / "processed"
        / run_id
        / f"analysis_version_{ANALYSIS_VERSION:03d}"
    )
    out_dir.mkdir(parents=True, exist_ok=True)

    # ------------------------------------------------------------------
    # Resolve processing params
    # ------------------------------------------------------------------
    proc_params = dbmod.load_processing_params(conn, sample_id)
    if proc_params:
        thickness_nm = proc_params.get("thickness_nm", _DEFAULT_THICKNESS_NM)
        area_map = proc_params.get("area_um2_by_size", _DEFAULT_AREA_UM2_BY_SIZE)
    else:
        thickness_nm = _DEFAULT_THICKNESS_NM
        area_map = _DEFAULT_AREA_UM2_BY_SIZE

    area_um2 = area_um2_for_device(device_id, area_map)

    # For iv_breakdown, data files live directly in raw_dir/data/ or raw_dir/
    # For impedance_cv_cf, CSVs live in <run_subdir>/data/
    if family == "iv_breakdown":
        data_dir = _find_data_dir(raw_dir)
    else:
        # The run subdirectory contains data/ with cf/cv CSVs
        data_dir = _find_impedance_data_dir(raw_dir)

    notebook_path = str(_NOTEBOOKS_DIR / f"{family}.ipynb")

    params = {
        "run_id": run_id,
        "data_dir": str(data_dir),
        "out_dir": str(out_dir),
        "area_um2": area_um2,
        "thickness_nm": thickness_nm,
        "device_id": device_id,
    }

    # ------------------------------------------------------------------
    # Execute notebook via papermill
    # ------------------------------------------------------------------
    try:
        nb_result = run_processing(family, params, notebook_path, str(out_dir))
    except Exception as exc:
        _transition(conn, run_id, "parsing", "parser_failed", worker_id,
                    f"pipeline-a: notebook execution failed: {exc}")
        return {"status": "parser_failed", "error": str(exc)}

    plots = nb_result["plots"]
    executed_nb = nb_result["notebook"]
    metrics = nb_result["metrics"]

    if not metrics:
        metrics = {}

    # ------------------------------------------------------------------
    # DB inserts: analysis_artifacts
    # ------------------------------------------------------------------
    for png_path in plots:
        sha = _sha256_file(Path(png_path))
        _insert_analysis_artifact(conn, run_id, ANALYSIS_VERSION, "plot", png_path, sha)

    nb_sha = _sha256_file(Path(executed_nb))
    _insert_analysis_artifact(conn, run_id, ANALYSIS_VERSION, "notebook", executed_nb, nb_sha)

    # parser_results row (minimal — no parsed columns for notebook path)
    _insert_notebook_parser_results(conn, run_id, family, metrics)

    # review_artifacts
    _insert_review_artifacts(
        conn, run_id,
        metrics_json=metrics,
        plots_json=plots,
        parser_warnings_json=[],
        llm_summary="",
    )

    # ------------------------------------------------------------------
    # State transitions: parsing → validated → processing → awaiting_review
    # ------------------------------------------------------------------
    _transition(conn, run_id, "parsing", "validated", worker_id,
                "pipeline-a: notebook parse succeeded")
    _transition(conn, run_id, "validated", "processing", worker_id,
                "pipeline-a: computing metrics")
    _transition(conn, run_id, "processing", "awaiting_review", worker_id,
                "pipeline-a: review artifacts written")

    # ------------------------------------------------------------------
    # Enqueue send_notification job
    # ------------------------------------------------------------------
    notif_payload_base = {
        "event_type": "awaiting_review",
        "run_id": run_id,
        "message": f"Run {run_id} ready for review ({measurement_type})",
        "photo_paths": plots,
    }
    try:
        notif_id = dbmod.insert_notification(conn, "awaiting_review", run_id, notif_payload_base)
        notif_payload = dict(notif_payload_base)
        notif_payload["notification_id"] = notif_id
        dbmod.enqueue_job(
            conn,
            "send_notification",
            run_id,
            f"notify:awaiting_review:{run_id}",
            notif_payload,
        )
    except Exception:
        # Notification enqueue failure is non-fatal — run is already awaiting_review
        pass

    return {
        "status": "awaiting_review",
        "metrics": metrics,
        "plots": plots,
        "notebook": executed_nb,
    }


def _find_data_dir(raw_dir: Path) -> Path:
    """
    Locate the data directory for VAC/IV runs.
    Checks raw_dir/data/ then raw_dir itself.
    """
    data_subdir = raw_dir / "data"
    if data_subdir.is_dir():
        return data_subdir
    return raw_dir


def _find_impedance_data_dir(raw_dir: Path) -> Path:
    """
    Locate the data/ directory for impedance runs.
    The structure is: raw_dir/<device>_run_<date>/data/*.csv
    Falls back to raw_dir/data/ or raw_dir.
    """
    # Look for a run subdirectory containing a data/ folder
    for sub in raw_dir.iterdir():
        if sub.is_dir():
            data_sub = sub / "data"
            if data_sub.is_dir():
                cf_files = list(data_sub.glob("*cf*.csv")) + list(data_sub.glob("*cv*.csv"))
                if cf_files:
                    return data_sub
    # Fallback
    data_subdir = raw_dir / "data"
    if data_subdir.is_dir():
        return data_subdir
    return raw_dir


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


def _insert_notebook_parser_results(
    conn: psycopg.Connection,
    run_id: str,
    family: str,
    metrics_dict: dict,
) -> None:
    """Insert a parser_results row for notebook-executed families."""
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
                json.dumps(metrics_dict),
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
