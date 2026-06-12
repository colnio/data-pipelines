"""
labdata.store — on-disk path resolver for JupyterHub notebooks.

Root is LABDATA_ROOT env var, default /srv/labdata.  Layout mirrors
internal/config/config.go (§5):

  <root>/raw/<sample>/<device>/<run_id>/
  <root>/processed/<run_id>/analysis_version_00N/
  <root>/published/<published_result_id>/

Sanitization must match Go's sanitizeSeg in internal/ingest/pipeline.go:
  - replace '/' and '\\' with '_'
  - strip surrounding whitespace
  - if empty / '.' / '..' → '_'
  - missing sample_id / device_id → '_unknown'
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Optional

# Import catalog lazily to avoid a hard import cycle when both are loaded at once
import labdata.catalog as _catalog
from labdata.parsers import fet_transfer as _fet


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

_DEFAULT_ROOT = "/srv/labdata"


def _root() -> Path:
    return Path(os.environ.get("LABDATA_ROOT", _DEFAULT_ROOT))


def _sanitize(s: Optional[str]) -> str:
    """Mirror Go sanitizeSeg: replace path separators, collapse edge cases."""
    if s is None:
        return "_"
    s = s.replace("/", "_").replace("\\", "_").strip()
    if s == "" or s == "." or s == "..":
        return "_"
    return s


def _sanitize_or_unknown(s: Optional[str]) -> str:
    """For sample/device fields: empty/None → '_unknown'."""
    if not s:
        return "_unknown"
    cleaned = _sanitize(s)
    if cleaned == "_":
        return "_unknown"
    return cleaned


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def raw_dir(sample_id: str, device_id: str, run_id: str) -> Path:
    """Return raw/<sample>/<device>/<run_id> path (not necessarily existing)."""
    return (
        _root()
        / "raw"
        / _sanitize_or_unknown(sample_id)
        / _sanitize_or_unknown(device_id)
        / _sanitize(run_id)
    )


def raw_files(run_id: str) -> list[Path]:
    """Return existing files under the raw dir for *run_id*.

    Looks up sample_id and device_id from catalog.get_run.
    """
    row = _catalog.get_run(run_id)
    d = raw_dir(row.get("sample_id"), row.get("device_id"), run_id)
    if not d.exists():
        return []
    return sorted(p for p in d.iterdir() if p.is_file())


def processed_dir(run_id: str, version: int = 1) -> Path:
    """Return processed/<run_id>/analysis_version_00N path."""
    ver_seg = f"analysis_version_{version:03d}"
    return _root() / "processed" / _sanitize(run_id) / ver_seg


def published_dir(published_result_id: str) -> Path:
    """Return published/<id> path."""
    return _root() / "published" / _sanitize(published_result_id)


def load_run_data(run_id: str) -> "_fet.ParseResult":
    """Read and parse the primary data file for *run_id*.

    Finds the raw directory via catalog.get_run, then picks the first file
    whose extension matches known data extensions (.data, .dat, .csv, .txt).
    Raises FileNotFoundError if no suitable file is found.
    Raises ValueError (propagated from the parser) if parsing fails.
    """
    row = _catalog.get_run(run_id)
    d = raw_dir(row.get("sample_id"), row.get("device_id"), run_id)

    _DATA_EXTS = {".data", ".dat", ".csv", ".txt"}

    candidates = sorted(
        p for p in d.iterdir()
        if p.is_file() and p.suffix.lower() in _DATA_EXTS
    ) if d.exists() else []

    # Also fall back to any file if extension list misses
    if not candidates and d.exists():
        candidates = sorted(p for p in d.iterdir() if p.is_file())

    if not candidates:
        raise FileNotFoundError(f"no data files in {d}")

    primary = candidates[0]
    text = primary.read_text(errors="replace")
    result = _fet.parse(text)
    return result
