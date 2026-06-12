export const meta = {
  name: 'labdata-jupyterhub',
  description: 'JupyterHub layer: labdata catalog/duck package, Hub authenticator+config, starter notebooks',
  phases: [{ title: 'Modules', detail: 'catalog package + hub config/authenticator + notebooks (parallel)' }],
}

const REPO = '/Users/colnio/go/data-pipelines'

const SUMMARY = {
  type: 'object',
  properties: {
    package: { type: 'string' },
    files_created: { type: 'array', items: { type: 'string' } },
    api_surface: { type: 'array', items: { type: 'string' } },
    tests_command: { type: 'string' },
    tests_passed: { type: 'boolean' },
    test_summary: { type: 'string' },
    integration_notes: { type: 'string' },
    notes: { type: 'string' },
  },
  required: ['package', 'files_created', 'tests_passed', 'test_summary'],
  additionalProperties: false,
}

// The labdata.* API is a FIXED CONTRACT shared by the catalog (producer) and
// notebooks (consumer) agents so they integrate without drift.
const LABDATA_API = `
labdata.catalog (read-only; connects as labdata_nb via env LABDATA_READONLY_URL, falling back to DATABASE_URL; queries ONLY the v_* views):
  list_runs(state=None, sample_id=None, device_id=None, measurement_type=None, limit=200) -> pandas.DataFrame
  get_run(run_id) -> dict            # single run row from v_runs, or raises KeyError
  run_files(run_id) -> pandas.DataFrame   # from v_run_files
  published_results(limit=200) -> pandas.DataFrame   # v_published_results
  samples() -> pandas.DataFrame      # v_samples
  devices(sample_id=None) -> pandas.DataFrame  # v_devices
  review_artifact(run_id) -> dict | None  # latest v_review_artifacts row (metrics_json etc.)
labdata.store (path resolver via env LABDATA_ROOT, default /srv/labdata; sanitization MUST match the Go pipeline: replace '/' and '\\\\' with '_', empty/'.'/'..' -> '_', missing sample/device -> '_unknown'):
  raw_dir(sample_id, device_id, run_id) -> pathlib.Path     # raw/<sample>/<device>/<run_id>
  raw_files(run_id) -> list[pathlib.Path]                    # resolves sample/device via catalog.get_run
  processed_dir(run_id, version=1) -> pathlib.Path           # processed/<run_id>/analysis_version_00N
  published_dir(published_result_id) -> pathlib.Path         # published/<id>
  load_run_data(run_id) -> labdata.parsers.fet_transfer.ParseResult   # reads + parses the primary file
labdata.duck:
  connect() -> duckdb connection with the catalog ATTACHed READ-ONLY as schema 'catalog'
              (so SQL can do: SELECT * FROM catalog.v_runs). Also exposes read_csv_auto/read_parquet over processed/published files.
labdata.seed:
  seed_demo(write_url=None, labdata_root=None) -> dict
      # Dev/test seeding. Using a WRITE connection (DATABASE_URL/lab), inserts sample 'DEMO1',
      # device 'demo_dev_1' (device_class 'fet'), a run 'demo-run-0001' driven to state 'published'
      # with run_files, a review_artifacts row (metrics_json with on_off_ratio etc.), a
      # published_results + published_artifacts row. Writes files under labdata_root:
      #   raw/DEMO1/demo_dev_1/demo-run-0001/iv_sweep.data  (small CSV: header 'V,I' then rows)
      #   processed/demo-run-0001/analysis_version_001/transfer.png (any small bytes)
      # Returns {'run_id','sample_id','device_id','raw_dir','published_result_id'}.
`

function rules(dir) {
  return `
REPO: ${REPO}. READ FIRST: ${REPO}/AGENTS.md ; ${REPO}/lab-data-system-architecture-updated.md (§18 JupyterHub, §22 security, §23 views, §5 layout) ; ${REPO}/migrations/00110_catalog_views.sql (the v_* views) ; ${REPO}/deploy/jupyterhub/readonly_role.sql (labdata_readonly / labdata_nb) ; ${REPO}/workers/labdata/db.py and parsers/fet_transfer.py (reuse) ; ${REPO}/internal/ingest/pipeline.go rawRunDir + internal/config/config.go path roots (match the on-disk layout EXACTLY) ; ${REPO}/workers/tests/test_pipeline_integration.py (reuse its DB-fixture pattern that creates a test DB and applies migrations by parsing goose Up blocks).
A local Postgres is running at localhost:5432 (user lab / pw lab); databases get migrated by the test fixtures. Work ONLY inside ${dir}. Do NOT edit Go files, go.mod, migrations, the root Makefile, or deploy/docker-compose.dev.yml. Do NOT run go commands.
DEFINITION OF DONE: your tests pass (exit 0). Report the exact command + pass/fail. gofmt is irrelevant; for Python run your tests in a venv you create.`
}

const catalogPrompt = `Make the Python worker package pip-installable as \`labdata\` and add the read-only catalog + DuckDB + seed helpers for JupyterHub notebooks (architecture §18). Work ONLY in ${REPO}/workers/.

Implement EXACTLY this API (the notebooks agent consumes it):
${LABDATA_API}

Deliverables in workers/:
- pyproject.toml: make the package installable as \`labdata\` (setuptools; packages = labdata + labdata.parsers; so \`pip install -e .\` from workers/ gives \`import labdata\`). Keep worker.py working.
- requirements.txt: ADD pandas, pyarrow, duckdb (keep existing psycopg[binary], numpy, matplotlib, pytest). If a wheel is unavailable for this Python, pick a working version and note it.
- labdata/catalog.py, labdata/store.py, labdata/duck.py, labdata/seed.py implementing the API above. catalog connects READ-ONLY (LABDATA_READONLY_URL else DATABASE_URL) and queries only v_* views. store sanitization MUST match the Go rawRunDir (read internal/ingest/pipeline.go). duck.connect() ATTACHes the catalog read-only (INSTALL/LOAD the duckdb 'postgres' extension; ATTACH '<ro dsn>' AS catalog (TYPE postgres, READ_ONLY)).
- workers/tests/test_catalog.py, test_store.py, test_duck.py, test_seed.py. Reuse the DB fixture from workers/tests/test_pipeline_integration.py BUT also: after migrating, apply ${REPO}/deploy/jupyterhub/readonly_role.sql with a substituted dev password and create labdata_nb; set LABDATA_READONLY_URL to the labdata_nb DSN; call seed_demo to populate; then test list_runs/get_run/run_files/published_results return the seeded rows, store path resolution + load_run_data parse the seeded file, and a duck.connect() query over catalog.v_runs returns rows. Skip cleanly if Postgres is unreachable.

Create a venv (python3 -m venv workers/.venv), pip install -r requirements.txt and \`pip install -e workers\`, then run pytest. ${rules(REPO + '/workers')}
Your test command: cd ${REPO} && TEST_DATABASE_URL=postgres://lab:lab@localhost:5432/labdata_test_catalog?sslmode=disable workers/.venv/bin/python -m pytest workers/tests -q
Report the catalog/store/duck/seed signatures actually implemented and the DB driver/extension notes.`

const hubPrompt = `Build the JupyterHub authenticator + config + dev container image + production runbook (architecture §18 critical interactive layer, §22). Work ONLY in ${REPO}/deploy/jupyterhub/ (the read-only SQL already lives there — do not modify readonly_role.sql).

Design constraint: production = TLJH (The Littlest JupyterHub, system users, SystemdSpawner); dev = a JupyterHub container (SimpleLocalProcessSpawner). The PORTABLE shared core is the authenticator + config; only the spawner differs. Auth is API-backed SSO against the Go API.

Deliverables in deploy/jupyterhub/:
- labdata_authenticator.py: \`class LabDataAuthenticator(jupyterhub.auth.LocalAuthenticator)\`. async authenticate(self, handler, data) POSTs {email,password} to \`\${LABDATA_API_URL}/v1/auth/login\` (env LABDATA_API_URL, default http://localhost:8080) using tornado.httpclient.AsyncHTTPClient. On HTTP 200 parse the JSON; return {'name': self.normalize_username(email), 'admin': user['global_role'] in ('admin','pi')}. On non-200 return None. Put the raw HTTP call in an overridable async method \`_login(self, email, password) -> dict|None\` so tests can monkeypatch it without a network. normalize_username(email): lowercase, take the part before '@', replace any char not [a-z0-9_-] with '_' (valid Linux username for prod create_system_users). Be import-safe.
- jupyterhub_config.py: set c.JupyterHub.authenticator_class to the LabDataAuthenticator; read LABDATA_API_URL; configure the jupyterhub-idle-culler service; per-user limits c.Spawner.mem_limit='2G', c.Spawner.cpu_limit=2; c.Spawner.default_url='/lab'; inject env into single-user servers: c.Spawner.environment = {LABDATA_ROOT, LABDATA_READONLY_URL, DATABASE_URL}. Choose spawner by env JUPYTERHUB_DEV: if set -> from jupyterhub.spawner import SimpleLocalProcessSpawner; c.JupyterHub.spawner_class = SimpleLocalProcessSpawner ; else leave the default (TLJH overrides with SystemdSpawner via its own config.d — add a commented block showing the prod SystemdSpawner + create_system_users=True settings). Guard imports so the file loads even if systemdspawner isn't installed.
- Dockerfile: FROM quay.io/jupyterhub/jupyterhub:5 . Build context is the REPO ROOT (so COPY can reach workers/). Steps: pip install jupyterlab jupyterhub-idle-culler duckdb pandas pyarrow matplotlib numpy 'psycopg[binary]' ; COPY workers /opt/workers ; pip install -e /opt/workers ; COPY deploy/jupyterhub/labdata_authenticator.py deploy/jupyterhub/jupyterhub_config.py /srv/jupyterhub/ ; set workdir /srv/jupyterhub. CMD jupyterhub -f /srv/jupyterhub/jupyterhub_config.py.
- compose-snippet.yml: the docker-compose service block to add (a 'jupyterhub' service: build context '..' (repo root) dockerfile deploy/jupyterhub/Dockerfile, ports 127.0.0.1:8000:8000, environment JUPYTERHUB_DEV=1, LABDATA_API_URL=http://host.docker.internal:8080, LABDATA_ROOT=/srv/labdata, LABDATA_READONLY_URL + DATABASE_URL pointing at the postgres service via host.docker.internal, extra_hosts host.docker.internal:host-gateway, volumes: repo notebooks -> /srv/labdata/notebooks, a named scratch volume rw, and host LABDATA raw/published mounted :ro; depends_on postgres). The orchestrator will merge this into deploy/docker-compose.dev.yml — provide it as a clean YAML snippet + a one-line note.
- test_authenticator.py: pytest that monkeypatches LabDataAuthenticator._login to assert: active user (login returns user dict) -> dict with name + admin flag; admin/pi role -> admin True, member -> admin False; failed login (_login returns None) -> authenticate returns None; normalize_username('Jane.Doe@nus.edu.sg') is a valid unix handle.
- TLJH.md: production runbook — install TLJH on Ubuntu; create the shared env (pip install the labdata package + duckdb + scientific stack into the TLJH user environment); drop labdata_authenticator.py + a jupyterhub_config.d snippet (authenticator + SystemdSpawner + create_system_users=True + resource limits + idle culler); run readonly_role.sql (substitute the password); the §22 Unix ownership model (raw/published 0444/0555 owned by the ingest account, group labdata-readers; per-user /srv/labdata/scratch/<user> writable; notebooks/{shared,teaching,examples} read-mostly); seed shared notebooks; back up shared notebooks not scratch.

Create a venv (python3 -m venv deploy/jupyterhub/.venv), pip install jupyterhub tornado pytest, run the authenticator test. ${rules(REPO + '/deploy/jupyterhub')}
Your test command: cd ${REPO} && deploy/jupyterhub/.venv/bin/python -m pytest deploy/jupyterhub/test_authenticator.py -q
Report the authenticator class/method signatures and the exact compose snippet for the orchestrator to merge.`

const notebooksPrompt = `Author the starter notebooks for JupyterHub (architecture §18: examples, teaching, reproducibility demo). Work ONLY in ${REPO}/notebooks/. Do NOT execute them against a database (the orchestrator runs them headless against a seeded DB later) — author correct, well-structured notebooks and validate them STRUCTURALLY (valid nbformat).

They use this FIXED labdata API (already being implemented in parallel; write code against it exactly):
${LABDATA_API}
Each notebook: a markdown intro cell + code cells, kernelspec python3, nbformat 4.x. Assume env LABDATA_READONLY_URL and LABDATA_ROOT are set. Keep outputs empty.

Deliverables in notebooks/:
- examples/01_browse_catalog.ipynb: import labdata.catalog; df = catalog.list_runs(); display; filter by state='published'; catalog.samples(); catalog.devices().
- examples/02_plot_a_run.ipynb: pick a published run id from catalog.list_runs(state='published'); res = labdata.store.load_run_data(run_id); matplotlib plot res.voltage vs res.current; show catalog.review_artifact(run_id) metrics.
- examples/03_published_results.ipynb: catalog.published_results(); for one, show the reproducibility receipt fields (manifest_hash, raw_file_hashes_json, parameter_version_ids_json, parser_version, published_by) and list its run_files with sha256.
- examples/04_duckdb_sql.ipynb: con = labdata.duck.connect(); con.sql("SELECT measurement_type, count(*) FROM catalog.v_runs GROUP BY 1"); a join example catalog.v_runs x catalog.v_published_results; and read_csv_auto over a processed/raw CSV path from labdata.store.
- teaching/parser_prototyping.ipynb: demonstrate the notebook->module promotion path (§18): read a raw file via labdata.store, prototype parsing with labdata.parsers.fet_transfer.parse + compute_metrics, then a markdown cell explaining how a validated prototype becomes a versioned pipeline module + job type.
- shared/reproducibility_demo.ipynb: take a published_result, re-read its raw files, recompute sha256, and assert they match raw_file_hashes_json — demonstrating reconstructability (the project's hard requirement).
- notebooks/README.md: the access model (read-only raw/published, writable scratch), the read-only DB role, and the promotion rule.

Validate every notebook parses with nbformat: create a venv (python3 -m venv notebooks/.venv), pip install nbformat, and run a tiny check script: for each .ipynb, nbformat.read(path, as_version=4) and nbformat.validate(nb) — must succeed for all. ${rules(REPO + '/notebooks')}
Your test command: cd ${REPO} && notebooks/.venv/bin/python -c "import nbformat,glob,sys; [nbformat.validate(nbformat.read(p,as_version=4)) for p in glob.glob('notebooks/**/*.ipynb',recursive=True)]; print('all notebooks valid')"
Report the notebook list and any labdata API calls used so the orchestrator can confirm they match.`

phase('Modules')
log('JupyterHub layer: catalog package + hub config/authenticator + notebooks (parallel Sonnet)')
const results = await parallel([
  () => agent(catalogPrompt,   { phase: 'Modules', label: 'catalog',   model: 'sonnet', schema: SUMMARY }),
  () => agent(hubPrompt,       { phase: 'Modules', label: 'hub',       model: 'sonnet', schema: SUMMARY }),
  () => agent(notebooksPrompt, { phase: 'Modules', label: 'notebooks', model: 'sonnet', schema: SUMMARY }),
])
return { results: results.filter(Boolean) }
