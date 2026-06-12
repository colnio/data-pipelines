"""
labdata.process_session — process_session job handler (Pipeline-A, session level).

Flow per job:
  1. Parse payload → run_id, sample_id, session_key, session_dir.
  2. Resolve processing params (thickness_nm, area_by_size) for the sample.
  3. Transition: promoted → parsing (tolerates any from-state).
  4. Run BOTH session notebooks over session_dir:
       - impedance_cv_cf (CV/CF summary)
       - iv_breakdown    (IV/breakdown summary)
     Each is run independently; tolerate a session missing one family.
     If BOTH produce no output, fail the run.
  5. Insert analysis_artifacts (plots + executed notebooks).
  6. Insert review_artifacts (combined metrics, all plots).
  7. Transition: parsing → validated → processing → awaiting_review.
  8. Insert notification + enqueue send_notification job.
  9. Return result dict.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from labdata import db as dbmod
from labdata.pipeline import (
    ANALYSIS_VERSION,
    WORKER_ACTOR_TYPE,
    _NOTEBOOKS_DIR,
    _insert_analysis_artifact,
    _insert_review_artifacts,
    _insert_notebook_parser_results,
    _sanitize_metrics_for_json,
    _sha256_file,
    _transition,
)
from labdata.seed import _DEFAULT_THICKNESS_NM, _DEFAULT_AREA_UM2_BY_SIZE


def handle_process_session(
    conn,
    job: dict,
    labdata_root: str,
    worker_id: str = "session-processor",
) -> dict:
    """
    Process a single process_session job.

    The job payload must contain:
      run_id       — synthetic "<sample_id>:<session_key>" run row id
      sample_id    — e.g. "9D66P1"
      session_key  — e.g. "2026-06-10"
      session_dir  — absolute path to the session directory on the server

    Returns a result dict suitable for complete_job().
    Raises on unexpected/unrecoverable errors.
    """
    from labdata.notebook_runner import run_session

    # ------------------------------------------------------------------
    # 1. Parse payload
    # ------------------------------------------------------------------
    payload: dict = job.get("payload_json") or {}
    run_id: str = payload.get("run_id") or job.get("run_id") or ""
    sample_id: str = payload.get("sample_id", "")
    session_key: str = payload.get("session_key", "")
    session_dir: str = payload.get("session_dir", "")

    if not run_id:
        raise ValueError(f"process_session job {job.get('id')}: missing run_id in payload")
    if not session_dir:
        raise ValueError(f"process_session job {job.get('id')}: missing session_dir in payload")

    # ------------------------------------------------------------------
    # 2. out_dir
    # ------------------------------------------------------------------
    out_dir = (
        Path(labdata_root)
        / "processed"
        / run_id
        / f"analysis_version_{ANALYSIS_VERSION:03d}"
    )
    out_dir.mkdir(parents=True, exist_ok=True)

    # ------------------------------------------------------------------
    # 3. Resolve processing params
    # ------------------------------------------------------------------
    proc_params = dbmod.load_processing_params(conn, sample_id)
    if proc_params:
        thickness_nm = proc_params.get("thickness_nm", _DEFAULT_THICKNESS_NM)
        area_map = proc_params.get("area_um2_by_size", _DEFAULT_AREA_UM2_BY_SIZE)
    else:
        thickness_nm = _DEFAULT_THICKNESS_NM
        area_map = _DEFAULT_AREA_UM2_BY_SIZE

    # ------------------------------------------------------------------
    # 4. Transition promoted → parsing (pass "" to tolerate any from-state)
    # ------------------------------------------------------------------
    _transition(conn, run_id, "", "parsing", worker_id,
                f"session: start processing {session_key}")

    # ------------------------------------------------------------------
    # 5. Run BOTH notebooks
    # ------------------------------------------------------------------
    title_base = f"{sample_id} / {session_key}"

    cf_result: dict[str, Any] | None = None
    iv_result: dict[str, Any] | None = None
    cf_error: str | None = None
    iv_error: str | None = None

    # 5a. impedance_cv_cf
    cf_params = {
        "sample_id": sample_id,
        "session_dir": session_dir,
        "out_dir": str(out_dir),
        "thickness_nm": thickness_nm,
        "area_by_size": area_map,
        "cal_freq_hz": 1e4,
        "title": title_base,
    }
    try:
        cf_result = run_session(
            family="impedance_cv_cf",
            params=cf_params,
            notebook_path=str(_NOTEBOOKS_DIR / "impedance_cv_cf.ipynb"),
            out_dir=str(out_dir),
        )
        # Tolerate runs where there are no CV/CF devices — no plots = skip family
        if not cf_result.get("plots") and not cf_result.get("metrics"):
            cf_result = None
    except Exception as exc:
        cf_error = str(exc)
        cf_result = None

    # 5b. iv_breakdown
    iv_params = {
        "sample_id": sample_id,
        "session_dir": session_dir,
        "out_dir": str(out_dir),
        "dataset_label": sample_id,
        "vbd_compliance_fraction": 0.5,
        "title": title_base,
    }
    try:
        iv_result = run_session(
            family="iv_breakdown",
            params=iv_params,
            notebook_path=str(_NOTEBOOKS_DIR / "iv_breakdown.ipynb"),
            out_dir=str(out_dir),
        )
        # Tolerate runs where there are no IV devices — no plots = skip family
        if not iv_result.get("plots") and not iv_result.get("metrics"):
            iv_result = None
    except Exception as exc:
        iv_error = str(exc)
        iv_result = None

    # If BOTH failed/produced nothing, fail the run
    if cf_result is None and iv_result is None:
        errors = []
        if cf_error:
            errors.append(f"impedance_cv_cf: {cf_error}")
        if iv_error:
            errors.append(f"iv_breakdown: {iv_error}")
        err_msg = "; ".join(errors) if errors else "both notebooks produced no output"
        _transition(conn, run_id, "parsing", "parser_failed", worker_id,
                    f"session: all notebooks failed: {err_msg}")
        return {"status": "parser_failed", "error": err_msg, "run_id": run_id}

    # ------------------------------------------------------------------
    # 6. Insert analysis_artifacts
    # ------------------------------------------------------------------
    all_plots: list[str] = []
    if cf_result:
        for png in cf_result.get("plots", []):
            sha = _sha256_file(Path(png))
            _insert_analysis_artifact(conn, run_id, ANALYSIS_VERSION, "plot", png, sha)
            all_plots.append(png)
        nb = cf_result.get("notebook", "")
        if nb and Path(nb).exists():
            nb_sha = _sha256_file(Path(nb))
            _insert_analysis_artifact(conn, run_id, ANALYSIS_VERSION, "notebook", nb, nb_sha)

    if iv_result:
        for png in iv_result.get("plots", []):
            sha = _sha256_file(Path(png))
            _insert_analysis_artifact(conn, run_id, ANALYSIS_VERSION, "plot", png, sha)
            all_plots.append(png)
        nb = iv_result.get("notebook", "")
        if nb and Path(nb).exists():
            nb_sha = _sha256_file(Path(nb))
            _insert_analysis_artifact(conn, run_id, ANALYSIS_VERSION, "notebook", nb, nb_sha)

    # ------------------------------------------------------------------
    # 7. Insert review_artifacts with combined metrics
    # ------------------------------------------------------------------
    cf_metrics = _sanitize_metrics_for_json(cf_result.get("metrics") or {}) if cf_result else {}
    iv_metrics = _sanitize_metrics_for_json(iv_result.get("metrics") or {}) if iv_result else {}
    combined_metrics: dict[str, Any] = {
        "cv_cf": cf_metrics,
        "iv_breakdown": iv_metrics,
    }

    _insert_review_artifacts(
        conn, run_id,
        metrics_json=combined_metrics,
        plots_json=all_plots,
        parser_warnings_json=[],
        llm_summary="",
    )

    # Also insert a parser_results row so the run is never empty
    _insert_notebook_parser_results(conn, run_id, "process_session", combined_metrics)

    # ------------------------------------------------------------------
    # 8. State transitions: parsing → validated → processing → awaiting_review
    # ------------------------------------------------------------------
    _transition(conn, run_id, "parsing", "validated", worker_id,
                "session: notebooks succeeded")
    _transition(conn, run_id, "validated", "processing", worker_id,
                "session: computing session metrics")
    _transition(conn, run_id, "processing", "awaiting_review", worker_id,
                "session: review artifacts written")

    # ------------------------------------------------------------------
    # 9. Enqueue review notification
    # ------------------------------------------------------------------
    notif_payload = {
        "event_type": "awaiting_review",
        "run_id": run_id,
        "message": f"Session {run_id} ready for review",
        "photo_paths": all_plots,
    }
    try:
        notif_id = dbmod.insert_notification(conn, "awaiting_review", run_id, notif_payload)
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

    # ------------------------------------------------------------------
    # 10. Return result
    # ------------------------------------------------------------------
    return {
        "status": "awaiting_review",
        "run_id": run_id,
        "plots": all_plots,
        "metrics": combined_metrics,
    }
