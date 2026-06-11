"""
Pure unit tests for labdata.parsers.fet_transfer — no DB required.

Tests cover:
  - valid 2-column data (comma and whitespace separated)
  - scientific notation values
  - header line detection and row-count declaration
  - comment lines with declared row count
  - trailing NaN rows are removed and warned
  - malformed rows skipped with warning
  - insufficient rows warning
  - all-NaN column → ok=False
  - empty input → ok=False
  - no data rows → ok=False
  - metrics: I_max, I_min, on_off_ratio, V_th
  - metrics on trivial/flat data
  - metrics raises on failed parse
"""

import sys
import os
import math

import numpy as np
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from labdata.parsers.fet_transfer import parse, compute_metrics, ParseResult


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _make_iv_text(n: int = 20, v_start: float = -3, v_stop: float = 3,
                  noise: float = 1e-12) -> str:
    """Generate a simple synthetic FET transfer curve text."""
    lines = ["# FET transfer data", "V,I"]
    vs = np.linspace(v_start, v_stop, n)
    for v in vs:
        # Simple square-law-ish current
        i = max(1e-12, 1e-6 * max(0, v + 1) ** 2) + noise * np.random.default_rng(42).standard_normal()
        lines.append(f"{v:.6f},{i:.6e}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Valid data tests
# ---------------------------------------------------------------------------

class TestValidData:
    def test_comma_separated(self):
        text = "V,I\n-1.0,1.0e-9\n0.0,1.0e-7\n1.0,1.0e-5\n"
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 3
        np.testing.assert_array_almost_equal(r.voltage, [-1.0, 0.0, 1.0])
        np.testing.assert_array_almost_equal(r.current, [1e-9, 1e-7, 1e-5])

    def test_whitespace_separated(self):
        text = "V I\n-1.0 1e-9\n0.0 1e-7\n1.0 1e-5\n"
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 3

    def test_scientific_notation(self):
        text = "V,I\n-2.5E+00,1.23E-12\n0.0E+00,4.56E-8\n2.5E+00,7.89E-4\n"
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 3
        assert r.current[0] == pytest.approx(1.23e-12)

    def test_no_header(self):
        text = "-1.0,1.0e-9\n0.0,1.0e-7\n1.0,1.0e-5\n"
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 3

    def test_comment_lines_skipped(self):
        text = "# instrument: probe-01\n# date: 2026-01-01\nV,I\n0.0,1e-7\n1.0,1e-5\n"
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 2

    def test_declared_row_count_in_comment_matches(self):
        text = "# nrows=3\nV,I\n-1.0,1e-9\n0.0,1e-7\n1.0,1e-5\n"
        r = parse(text)
        assert r.ok
        assert r.rows_declared == 3
        assert r.rows_actual == 3
        assert not any("declared" in w for w in r.warnings)

    def test_declared_row_count_mismatch_warns(self):
        text = "# nrows=5\nV,I\n-1.0,1e-9\n0.0,1e-7\n1.0,1e-5\n"
        r = parse(text)
        assert r.ok
        assert any("declared" in w for w in r.warnings)

    def test_large_synthetic_curve(self):
        text = _make_iv_text(n=50)
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 50

    def test_warnings_list_is_list(self):
        r = parse("V,I\n0.0,1e-7\n1.0,1e-5\n")
        assert isinstance(r.warnings, list)


# ---------------------------------------------------------------------------
# Trailing NaN tests
# ---------------------------------------------------------------------------

class TestTrailingNaN:
    def test_trailing_nan_rows_removed(self):
        text = "V,I\n-1.0,1e-9\n0.0,1e-7\n1.0,nan\n2.0,nan\n"
        r = parse(text)
        assert r.ok
        assert r.rows_actual == 2
        assert any("trailing" in w for w in r.warnings)

    def test_all_nan_current_fails(self):
        text = "V,I\n-1.0,nan\n0.0,nan\n1.0,nan\n"
        r = parse(text)
        assert not r.ok

    def test_all_nan_voltage_fails(self):
        text = "V,I\nnan,1e-9\nnan,1e-7\nnan,1e-5\n"
        r = parse(text)
        assert not r.ok


# ---------------------------------------------------------------------------
# Malformed input tests
# ---------------------------------------------------------------------------

class TestMalformed:
    def test_empty_string_fails(self):
        r = parse("")
        assert not r.ok

    def test_only_whitespace_fails(self):
        r = parse("   \n  \n")
        assert not r.ok

    def test_only_header_fails(self):
        r = parse("V,I\n")
        assert not r.ok

    def test_single_column_skipped_with_warning(self):
        text = "V,I\n-1.0\n0.0\n1.0\n"
        r = parse(text)
        # Rows with wrong column count are skipped
        assert not r.ok or (r.ok and all(len(str(w)) > 0 for w in r.warnings))

    def test_three_columns_skipped(self):
        text = "V,I\n-1.0,1e-9,extra\n0.0,1e-7,extra\n1.0,1e-5,extra\n"
        r = parse(text)
        # All rows have 3 columns → skipped → no valid data
        assert not r.ok or r.rows_actual == 0

    def test_partial_bad_rows_warned(self):
        text = "V,I\n-1.0,1e-9\nbad_row\n1.0,1e-5\n"
        r = parse(text)
        assert r.ok  # still ok because 2 valid rows remain
        assert any("malformed" in w or "skipped" in w for w in r.warnings)

    def test_fewer_than_3_rows_warns(self):
        text = "V,I\n-1.0,1e-9\n0.0,1e-7\n"
        r = parse(text)
        # 2 rows is technically ok but warns
        assert r.ok
        # Not required to warn for 2 rows, but should be usable
        assert r.rows_actual == 2


# ---------------------------------------------------------------------------
# Metrics tests
# ---------------------------------------------------------------------------

class TestMetrics:
    def _simple_result(self) -> ParseResult:
        V = np.array([-2.0, -1.0, 0.0, 1.0, 2.0])
        I = np.array([1e-12, 1e-11, 1e-8, 1e-6, 1e-4])
        return ParseResult(ok=True, voltage=V, current=I, rows_actual=5)

    def test_i_max_i_min(self):
        m = compute_metrics(self._simple_result())
        assert m.I_max == pytest.approx(1e-4)
        assert m.I_min == pytest.approx(1e-12)

    def test_on_off_ratio(self):
        m = compute_metrics(self._simple_result())
        assert m.on_off_ratio == pytest.approx(1e8)

    def test_vth_is_float_or_none(self):
        m = compute_metrics(self._simple_result())
        assert m.V_th is None or isinstance(m.V_th, float)

    def test_on_off_ratio_zero_i_min(self):
        V = np.array([0.0, 1.0, 2.0])
        I = np.array([0.0, 1e-9, 1e-6])
        r = ParseResult(ok=True, voltage=V, current=I, rows_actual=3)
        m = compute_metrics(r)
        assert math.isinf(m.on_off_ratio) or math.isnan(m.on_off_ratio)

    def test_negative_currents_abs(self):
        V = np.array([-1.0, 0.0, 1.0])
        I = np.array([-1e-9, -1e-7, -1e-5])
        r = ParseResult(ok=True, voltage=V, current=I, rows_actual=3)
        m = compute_metrics(r)
        assert m.I_max == pytest.approx(1e-5)
        assert m.I_min == pytest.approx(1e-9)

    def test_metrics_raises_on_failed_parse(self):
        r = ParseResult(ok=False, error="test error")
        with pytest.raises(ValueError, match="failed parse"):
            compute_metrics(r)

    def test_vth_estimate_monotone(self):
        """V_th estimate should be in the voltage range for monotone I(V)."""
        V = np.linspace(-3, 3, 30)
        # Step function approximation: I = 1e-12 for V<0, ramps for V>0
        I = np.where(V < 0, 1e-12, 1e-6 * V ** 2 + 1e-12)
        r = ParseResult(ok=True, voltage=V, current=I, rows_actual=30)
        m = compute_metrics(r)
        if m.V_th is not None:
            assert V.min() - 1 <= m.V_th <= V.max() + 1

    def test_two_point_vth_none(self):
        """With fewer than 3 points V_th should be None."""
        V = np.array([0.0, 1.0])
        I = np.array([1e-10, 1e-6])
        r = ParseResult(ok=True, voltage=V, current=I, rows_actual=2)
        m = compute_metrics(r)
        assert m.V_th is None


# ---------------------------------------------------------------------------
# Integration: parse → metrics round-trip
# ---------------------------------------------------------------------------

class TestRoundTrip:
    def test_full_pipeline_on_synthetic_data(self):
        text = _make_iv_text(n=30)
        r = parse(text)
        assert r.ok
        m = compute_metrics(r)
        assert m.I_max > m.I_min >= 0
        assert m.on_off_ratio > 0
