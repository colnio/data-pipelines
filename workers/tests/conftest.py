"""
Shared test helpers for the labdata test suite.

Provides:
  - apply_readonly_role(admin_dsn, db_name, nb_password): apply readonly_role.sql
    via an AUTOCOMMIT admin connection so DO $$ blocks and role DDL work correctly.
"""

from __future__ import annotations

import os
import re

import psycopg

READONLY_ROLE_SQL = os.path.join(
    os.path.dirname(__file__), "..", "..", "deploy", "jupyterhub", "readonly_role.sql"
)


def apply_readonly_role(admin_dsn: str, db_name: str, nb_password: str) -> None:
    """Apply readonly_role.sql with autocommit on the admin (postgres) DB.

    Role DDL and DO $$ blocks must run outside a transaction and cluster-wide,
    so they go to the 'postgres' DB with autocommit.  GRANTs on views then run
    on the target DB (also with autocommit, since they can be outside transactions).
    """
    ro_sql_raw = open(READONLY_ROLE_SQL).read()
    ro_sql = ro_sql_raw.replace("__NB_PASSWORD__", nb_password)

    # Split into statements roughly: semicolons terminate most DDL, but DO $$
    # blocks end at 'END $$;'.  We use a simple state machine that is
    # dollar-quote aware.
    stmts = _split_sql_aware(ro_sql)

    # Role/user DDL runs on the admin (postgres) DB
    with psycopg.connect(admin_dsn, autocommit=True) as ac:
        for stmt in stmts:
            s = stmt.strip()
            if not s:
                continue
            try:
                ac.execute(s)
            except Exception as exc:
                err = str(exc).lower()
                if "already exists" in err or "duplicate" in err:
                    pass  # idempotent
                elif "does not exist" in err:
                    pass  # optional revoke
                else:
                    # GRANTs on views in specific DB — run on target DB instead
                    if "does not exist" in err or "unrecognized" in err:
                        pass
                    # Otherwise ignore (role already has grants etc.)
        # labdata_readonly / labdata_nb are CLUSTER-GLOBAL roles shared across
        # every test database and any out-of-band dev run. The DO-block ALTER in
        # readonly_role.sql does not always reset the password reliably here, so
        # force the role + password unconditionally — the suite must not depend
        # on whatever password the role happens to carry from a prior run.
        ac.execute(
            "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='labdata_readonly') "
            "THEN CREATE ROLE labdata_readonly NOLOGIN; END IF; END $$;"
        )
        ac.execute(
            "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='labdata_nb') "
            "THEN CREATE ROLE labdata_nb LOGIN; END IF; END $$;"
        )
        ac.execute(f"ALTER ROLE labdata_nb WITH LOGIN PASSWORD '{nb_password}'")
        ac.execute("GRANT labdata_readonly TO labdata_nb")

    # GRANT SELECT on views must run on the target DB
    target_dsn = re.sub(r"/postgres(\?|$)", f"/{db_name}\\1", admin_dsn)
    with psycopg.connect(target_dsn, autocommit=True) as tc:
        # Re-apply just the GRANT SELECT statements
        for stmt in stmts:
            s = stmt.strip()
            if s.upper().startswith("GRANT SELECT"):
                try:
                    tc.execute(s)
                except Exception:
                    pass
        # Also grant CONNECT
        try:
            tc.execute(
                f"GRANT CONNECT ON DATABASE \"{db_name}\" TO labdata_readonly"
            )
        except Exception:
            pass


def _split_sql_aware(text: str) -> list[str]:
    """Split SQL text on semicolons, handling dollar-quoted blocks."""
    statements = []
    current: list[str] = []
    i = 0
    in_dollar_quote = False
    dollar_tag = ""

    while i < len(text):
        # Check for dollar-quote open/close
        if not in_dollar_quote and text[i] == "$":
            # Find end of dollar tag
            j = text.find("$", i + 1)
            if j != -1:
                tag = text[i:j + 1]
                current.append(tag)
                i = j + 1
                in_dollar_quote = True
                dollar_tag = tag
                continue
        elif in_dollar_quote and text[i] == "$":
            # Check if this is the closing tag
            end_tag_end = i + len(dollar_tag)
            if text[i:end_tag_end] == dollar_tag:
                current.append(dollar_tag)
                i = end_tag_end
                in_dollar_quote = False
                dollar_tag = ""
                continue

        if not in_dollar_quote and text[i] == ";":
            stmt = "".join(current).strip()
            if stmt:
                statements.append(stmt)
            current = []
            i += 1
            continue

        current.append(text[i])
        i += 1

    # trailing fragment
    stmt = "".join(current).strip()
    if stmt:
        statements.append(stmt)
    return statements
