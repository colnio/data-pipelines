"""
process_session_demo.py — run BOTH session notebooks over a real session dir.

Usage:
    cd workers
    .venv/bin/python scripts/process_session_demo.py

Runs impedance_cv_cf and iv_breakdown notebooks over:
    sample_data/9D66P1/2026-06-10/
Writes results to /tmp/9d66p1-out/.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

# Allow running as a module or as a script
REPO_ROOT = Path(__file__).parent.parent.parent
WORKERS_DIR = Path(__file__).parent.parent
sys.path.insert(0, str(WORKERS_DIR))

from labdata.notebook_runner import run_session

# ---------------------------------------------------------------------------
# Configuration (matching sample_output/props)
# ---------------------------------------------------------------------------

SAMPLE_ID = "9D66P1"
SESSION_DIR = str(REPO_ROOT / "sample_data" / "9D66P1" / "2026-06-10")
OUT_DIR = "/tmp/9d66p1-out"
THICKNESS_NM = 2.5
AREA_BY_SIZE = {
    "big": 771786,
    "mid": 192180,
    "small": 67400,
    "little": 28508,
    "tiny": 6333,
}
NOTEBOOKS_DIR = WORKERS_DIR / "notebooks"


def main() -> None:
    out_path = Path(OUT_DIR)
    out_path.mkdir(parents=True, exist_ok=True)

    print("=" * 60)
    print(f"Session   : {SESSION_DIR}")
    print(f"Output    : {OUT_DIR}")
    print("=" * 60)

    # ------------------------------------------------------------------
    # 1) Impedance / C(V) / C(F)
    # ------------------------------------------------------------------
    print("\n[1/2] Running impedance_cv_cf notebook...")
    cf_result = run_session(
        family="impedance_cv_cf",
        params={
            "sample_id": SAMPLE_ID,
            "session_dir": SESSION_DIR,
            "out_dir": OUT_DIR,
            "thickness_nm": THICKNESS_NM,
            "area_by_size": AREA_BY_SIZE,
            "cal_freq_hz": 1e4,
            "title": f"{SAMPLE_ID} — C(V)/C(F) summary",
        },
        notebook_path=str(NOTEBOOKS_DIR / "impedance_cv_cf.ipynb"),
        out_dir=OUT_DIR,
    )

    print(f"  Notebook : {cf_result['notebook']}")
    print(f"  Plots    : {cf_result['plots']}")
    print(f"  Metrics  : {json.dumps(cf_result['metrics'], indent=4)}")

    # ------------------------------------------------------------------
    # 2) IV / Breakdown
    # ------------------------------------------------------------------
    print("\n[2/2] Running iv_breakdown notebook...")
    iv_result = run_session(
        family="iv_breakdown",
        params={
            "sample_id": SAMPLE_ID,
            "session_dir": SESSION_DIR,
            "out_dir": OUT_DIR,
            "dataset_label": SAMPLE_ID,
            "vbd_current_threshold_a": 1e-2,
            "vbd_compliance_fraction": 0.5,
            "title": f"{SAMPLE_ID} — IV breakdown summary",
        },
        notebook_path=str(NOTEBOOKS_DIR / "iv_breakdown.ipynb"),
        out_dir=OUT_DIR,
    )

    print(f"  Notebook : {iv_result['notebook']}")
    print(f"  Plots    : {iv_result['plots']}")
    print(f"  Metrics  : {json.dumps(iv_result['metrics'], indent=4)}")

    # ------------------------------------------------------------------
    # Sanity checks
    # ------------------------------------------------------------------
    print("\n" + "=" * 60)
    print("SANITY CHECKS")
    ok = True

    cf_png = out_path / "cv_cf_summary.png"
    iv_png = out_path / "iv_breakdown_summary.png"

    for png in (cf_png, iv_png):
        size = png.stat().st_size if png.exists() else 0
        status = "OK" if size > 20_000 else "FAIL (too small or missing)"
        print(f"  {png.name}: {size:,} bytes — {status}")
        if size <= 20_000:
            ok = False

    cf_m = cf_result["metrics"]
    if cf_m:
        mean_k = cf_m.get("mean_k", 0)
        status = "OK" if 1.0 < mean_k < 5.0 else f"FAIL (out of range)"
        print(f"  mean_k = {mean_k:.3f} — {status}")
        if not (1.0 < mean_k < 5.0):
            ok = False

    iv_m = iv_result["metrics"]
    if iv_m:
        vbd_avg = iv_m.get("vbd_avg", -1)
        status = "OK" if 0.5 < vbd_avg < 5.0 else "FAIL (out of range)"
        print(f"  vbd_avg = {vbd_avg:.3f} V — {status}")
        if not (0.5 < vbd_avg < 5.0):
            ok = False

    print("=" * 60)
    if ok:
        print("All checks passed.")
    else:
        print("One or more checks FAILED.")
        sys.exit(1)


if __name__ == "__main__":
    main()
