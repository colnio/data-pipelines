# Pipeline-A Worker

Python worker that claims `parse_run` jobs from the shared Postgres jobs table,
parses FET transfer-curve data, computes metrics, renders plots, writes
scientific outputs, and drives a run through the state machine to
`awaiting_review`.

## Requirements

- Python 3.10+ (tested on 3.14)
- Postgres 14+ accessible at `DATABASE_URL` (default: `postgres://lab:lab@localhost:5432/labdata`)
- Schema applied by Go migrations under `../migrations/`

## Setup

```sh
cd workers/
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
```

## Running the worker

```sh
export DATABASE_URL=postgres://lab:lab@localhost:5432/labdata?sslmode=disable
export LABDATA_ROOT=/srv/labdata
export WORKER_ID=pipeline-a-1          # optional

.venv/bin/python worker.py
```

The worker polls every 2 s when the queue is empty. Press Ctrl-C for a clean
shutdown; the current job (if any) finishes before the process exits.

## Running tests

### Unit tests only (no DB required)

```sh
.venv/bin/python -m pytest workers/tests/test_fet_transfer.py -v
```

### Full suite (requires Postgres)

The integration tests create a throwaway database (`labdata_test_py` by
default) and apply all Go migrations themselves. The database is recreated on
the first run only.

```sh
TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable \
  .venv/bin/python -m pytest workers/tests -v
```

Override the test database:

```sh
TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/my_test_db?sslmode=disable \
  .venv/bin/python -m pytest workers/tests -v
```

If Postgres is unreachable, integration tests are automatically skipped.

## Package layout

```
workers/
  requirements.txt         pinned dependencies
  worker.py                entrypoint / poll loop
  labdata/
    __init__.py
    db.py                  Postgres helpers: claim_job, complete_job,
                           fail_job, transition (→ transition_run())
    pipeline.py            parse_run handler (Pipeline-A)
    parsers/
      __init__.py
      fet_transfer.py      FET transfer-curve parser + metrics
  tests/
    test_fet_transfer.py   pure unit tests (no DB)
    test_pipeline_integration.py  DB integration tests
```

## DB driver note

`psycopg[binary]==3.3.4` installs cleanly on Python 3.14 / macOS arm64.
If the binary wheel is unavailable, replace with `pg8000` (pure Python) and
update `labdata/db.py` to use `pg8000.connect`.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://lab:lab@localhost:5432/labdata?sslmode=disable` | Worker DB |
| `LABDATA_ROOT` | `/srv/labdata` | Root of the lab data tree |
| `WORKER_ID` | `pipeline-a-<pid>` | Unique worker identifier |
| `TEST_DATABASE_URL` | `postgres://lab:lab@localhost:5432/labdata_test_py?sslmode=disable` | Integration test DB |
