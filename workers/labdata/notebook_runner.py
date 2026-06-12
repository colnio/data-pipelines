"""
labdata.notebook_runner — papermill-based notebook execution harness.

run_processing(family, params, notebook_path, out_dir) -> dict
  Execute a parameterized Jupyter notebook with papermill, collect the
  produced PNG plots and metrics.json, return a result dict.

The executed notebook is saved to <out_dir>/processed.ipynb.
Each processing notebook writes a metrics.json to out_dir upon completion.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any


def run_processing(
    family: str,
    params: dict[str, Any],
    notebook_path: str,
    out_dir: str,
) -> dict[str, Any]:
    """
    Execute the notebook at notebook_path with papermill, injecting params.

    Parameters
    ----------
    family:
        Logical processing family name (e.g. 'iv_breakdown', 'impedance_cv_cf').
        Used only for logging / result tagging.
    params:
        Dict passed verbatim to papermill as notebook parameters.
        Must include at least: run_id, data_dir, out_dir.
    notebook_path:
        Absolute path to the source .ipynb template notebook.
    out_dir:
        Directory where the executed notebook and outputs are written.

    Returns
    -------
    dict with keys:
        plots    — list of absolute PNG paths found in out_dir after execution
        notebook — absolute path to the executed notebook (processed.ipynb)
        metrics  — dict loaded from out_dir/metrics.json (empty dict if missing)
    """
    import papermill as pm

    out_path = Path(out_dir)
    out_path.mkdir(parents=True, exist_ok=True)

    executed_nb = out_path / "processed.ipynb"

    # Ensure out_dir is in params so the notebook can write there
    run_params = dict(params)
    run_params.setdefault("out_dir", out_dir)

    pm.execute_notebook(
        input_path=notebook_path,
        output_path=str(executed_nb),
        parameters=run_params,
        kernel_name="python3",
        progress_bar=False,
    )

    # Collect PNG plots produced in out_dir
    plots = sorted(str(p) for p in out_path.glob("*.png"))

    # Load metrics.json written by the notebook
    metrics_file = out_path / "metrics.json"
    metrics: dict[str, Any] = {}
    if metrics_file.exists():
        try:
            metrics = json.loads(metrics_file.read_text())
        except Exception:
            metrics = {}

    return {
        "plots": plots,
        "notebook": str(executed_nb),
        "metrics": metrics,
    }
