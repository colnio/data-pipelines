# JupyterHub dev-container image for the Lab Data system.
#
# Build context: REPO ROOT (so COPY can reach workers/).
#   docker build -f deploy/jupyterhub/Dockerfile .
#
# Architecture §18 (critical interactive layer) — dev path.
# Production uses TLJH with SystemdSpawner; see TLJH.md.

FROM quay.io/jupyterhub/jupyterhub:5

# ------------------------------------------------------------------
# Scientific + lab-data Python stack
# ------------------------------------------------------------------
RUN pip install --no-cache-dir \
        jupyterlab \
        jupyterhub-idle-culler \
        duckdb \
        pandas \
        pyarrow \
        matplotlib \
        numpy \
        "psycopg[binary]"

# ------------------------------------------------------------------
# Lab-data workers package (contains labdata.db helpers, parsers, etc.)
# ------------------------------------------------------------------
COPY workers /opt/workers
RUN pip install --no-cache-dir -e /opt/workers

# ------------------------------------------------------------------
# JupyterHub config + authenticator
# ------------------------------------------------------------------
COPY deploy/jupyterhub/labdata_authenticator.py /srv/jupyterhub/labdata_authenticator.py
COPY deploy/jupyterhub/jupyterhub_config.py     /srv/jupyterhub/jupyterhub_config.py

WORKDIR /srv/jupyterhub

# ------------------------------------------------------------------
# Launch
# ------------------------------------------------------------------
CMD ["jupyterhub", "-f", "/srv/jupyterhub/jupyterhub_config.py"]
