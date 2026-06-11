"""
labdata.parsers.fet_transfer — FET transfer-curve (I-V) parser and metrics.

File format (architecture §17):
  - Optional comment/header lines starting with '#' are skipped.
  - First non-comment line may be a column-header row ("V,I" or "Vg,Id" etc.).
  - Remaining lines are numeric data rows: two columns separated by comma or
    whitespace; scientific notation is accepted.
  - Trailing all-NaN rows are rejected.

Validation checks (architecture §17):
  - Exactly 2 columns expected.
  - Header-declared row count vs actual (if the header contains a digit token
    that looks like "nrows=N" or a bare integer).
  - No all-NaN trailing rows.
  - Minimum 3 data points for meaningful analysis.
  - At least one non-NaN value in each column.

Metrics computed:
  - I_max, I_min
  - on/off current ratio (I_max / I_min, guarded against zero)
  - threshold voltage V_th — estimated as the gate voltage at the point of
    maximum transconductance dI/dV (linear extrapolation intercept).
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Optional

import numpy as np

# ---------------------------------------------------------------------------
# Data structures
# ---------------------------------------------------------------------------

PARSER_VERSION = "fet_transfer_v1.0"
COLUMNS_EXPECTED = 2


@dataclass
class ParseResult:
    """Outcome of parsing one FET transfer file."""
    ok: bool
    voltage: np.ndarray = field(default_factory=lambda: np.array([]))
    current: np.ndarray = field(default_factory=lambda: np.array([]))
    rows_declared: Optional[int] = None   # from header if present
    rows_actual: int = 0
    warnings: list[str] = field(default_factory=list)
    error: Optional[str] = None           # set only when ok=False


@dataclass
class Metrics:
    """Scalar summary metrics derived from a valid parse result."""
    I_max: float
    I_min: float
    on_off_ratio: float          # I_max / I_min (or inf / nan when I_min==0)
    V_th: Optional[float]        # threshold voltage estimate


# ---------------------------------------------------------------------------
# Parser
# ---------------------------------------------------------------------------

def parse(text: str) -> ParseResult:
    """
    Parse the text content of a FET transfer data file.

    Returns a ParseResult.  ok=True means the data is usable.  ok=False means
    a fatal format error was found.  Warnings are non-fatal observations.
    """
    warnings: list[str] = []
    rows_declared: Optional[int] = None
    data_lines: list[str] = []
    in_header = True

    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            continue

        # Comment lines
        if line.startswith("#"):
            _check_declared_rows(line, warnings)
            if rows_declared is None:
                rows_declared = _extract_row_count(line)
            continue

        # First non-comment, non-empty line: decide if it is a column header
        # (non-numeric first token) or data.
        if in_header:
            in_header = False
            if _is_header_line(line):
                # May embed row-count declaration
                if rows_declared is None:
                    rows_declared = _extract_row_count(line)
                continue  # skip header row

        data_lines.append(line)

    if not data_lines:
        return ParseResult(ok=False, error="no data rows found")

    # Parse numeric data
    voltages: list[float] = []
    currents: list[float] = []
    bad_rows: list[int] = []

    for i, line in enumerate(data_lines):
        parts = re.split(r"[,\s]+", line.strip())
        parts = [p for p in parts if p]
        if len(parts) != COLUMNS_EXPECTED:
            bad_rows.append(i + 1)
            continue
        try:
            v = float(parts[0])
            c = float(parts[1])
        except ValueError:
            bad_rows.append(i + 1)
            continue
        voltages.append(v)
        currents.append(c)

    if bad_rows:
        warnings.append(
            f"skipped {len(bad_rows)} malformed row(s) at line(s): "
            + ", ".join(str(r) for r in bad_rows[:10])
        )

    if not voltages:
        return ParseResult(ok=False, error="no valid numeric rows after parsing")

    V = np.asarray(voltages, dtype=float)
    I = np.asarray(currents, dtype=float)
    rows_actual = len(V)

    # Check declared vs actual
    if rows_declared is not None and rows_declared != rows_actual:
        warnings.append(
            f"declared row count {rows_declared} != actual {rows_actual}"
        )

    # Reject all-NaN trailing rows
    trailing_nan_count = 0
    for idx in range(len(V) - 1, -1, -1):
        if np.isnan(V[idx]) or np.isnan(I[idx]):
            trailing_nan_count += 1
        else:
            break
    if trailing_nan_count > 0:
        warnings.append(f"dropping {trailing_nan_count} trailing all-NaN row(s)")
        V = V[: rows_actual - trailing_nan_count]
        I = I[: rows_actual - trailing_nan_count]
        rows_actual = len(V)

    if rows_actual == 0:
        return ParseResult(ok=False, error="all rows are NaN")

    # Minimum data check
    if rows_actual < 3:
        warnings.append(f"only {rows_actual} data point(s); metrics may be unreliable")

    # Sanity: at least one non-NaN in each column
    if np.all(np.isnan(V)):
        return ParseResult(ok=False, error="voltage column is entirely NaN")
    if np.all(np.isnan(I)):
        return ParseResult(ok=False, error="current column is entirely NaN")

    return ParseResult(
        ok=True,
        voltage=V,
        current=I,
        rows_declared=rows_declared,
        rows_actual=rows_actual,
        warnings=warnings,
    )


# ---------------------------------------------------------------------------
# Metrics
# ---------------------------------------------------------------------------

def compute_metrics(result: ParseResult) -> Metrics:
    """
    Compute FET transfer-curve scalar metrics from a successful ParseResult.

    Raises ValueError if result.ok is False or arrays are empty.
    """
    if not result.ok:
        raise ValueError("cannot compute metrics on a failed parse result")

    V = result.voltage
    I = result.current

    # Remove NaN pairs for metrics
    mask = ~(np.isnan(V) | np.isnan(I))
    V = V[mask]
    I = I[mask]

    if len(I) == 0:
        raise ValueError("no valid (non-NaN) current values for metrics")

    I_abs = np.abs(I)
    I_max = float(np.max(I_abs))
    I_min = float(np.min(I_abs))

    if I_min == 0.0:
        on_off_ratio = float("inf") if I_max > 0 else float("nan")
    else:
        on_off_ratio = I_max / I_min

    V_th = _estimate_vth(V, I)

    return Metrics(
        I_max=I_max,
        I_min=I_min,
        on_off_ratio=on_off_ratio,
        V_th=V_th,
    )


def _estimate_vth(V: np.ndarray, I: np.ndarray) -> Optional[float]:
    """
    Threshold-voltage estimate using the maximum-transconductance (gm) method:

      1. Compute dI/dV (central differences).
      2. Find the index of |gm_max|.
      3. Linear fit (tangent line) at that point: I = gm * (V - V_th).
         V_th = V_at_gm_max - I_at_gm_max / gm_max

    Returns None if the data is too short or degenerate.
    """
    if len(V) < 3:
        return None

    # Sort by voltage for sensible differentiation
    order = np.argsort(V)
    Vs = V[order]
    Is = I[order]

    # Central differences (np.gradient handles endpoints with one-sided diff)
    gm = np.gradient(Is, Vs)

    # Largest absolute transconductance
    idx = int(np.argmax(np.abs(gm)))
    gm_at_idx = gm[idx]

    if gm_at_idx == 0.0:
        return None

    V_th = float(Vs[idx] - Is[idx] / gm_at_idx)
    return V_th


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _is_header_line(line: str) -> bool:
    """
    Return True if the line looks like a column-name header rather than data.
    A header has at least one token that cannot be parsed as a float.
    """
    parts = re.split(r"[,\s]+", line.strip())
    parts = [p for p in parts if p]
    if not parts:
        return False
    for p in parts:
        try:
            float(p)
        except ValueError:
            return True
    return False


def _extract_row_count(line: str) -> Optional[int]:
    """
    Look for a declared row count in a header/comment line.
    Patterns recognised:
      - "nrows=123"  or  "rows=123"  (case-insensitive)
      - A bare integer token standing alone (e.g. "# 500")
    """
    # Explicit key=value
    m = re.search(r"\b(?:n_?rows?)\s*[=:]\s*(\d+)", line, re.IGNORECASE)
    if m:
        return int(m.group(1))
    # Bare integer in comment
    m = re.match(r"^#\s*(\d+)\s*$", line)
    if m:
        return int(m.group(1))
    return None


def _check_declared_rows(line: str, warnings: list[str]) -> None:
    """Placeholder hook for future cross-field comment validation."""
    pass
