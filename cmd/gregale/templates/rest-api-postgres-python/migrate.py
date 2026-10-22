"""Release-only schema application; never used by serving startup."""

import os
from pathlib import Path

import psycopg
from database import connection_kwargs


def migrate(uri):
    if not uri:
        raise ValueError("Migration credentials are required")
    with psycopg.connect(uri, **connection_kwargs(uri)) as connection:
        connection.execute("SET statement_timeout = '120s'")
        connection.execute("SET lock_timeout = '30s'")
        with connection.transaction():
            connection.execute("SELECT pg_advisory_xact_lock(734928146)")
            connection.execute(Path(__file__).with_name("schema.sql").read_text())


if __name__ == "__main__":
    try:
        migrate(os.environ.get("MIGRATION_DATABASE_URL"))
        print("Notes schema ready")
    except Exception:
        print("Migration failed: check MIGRATION_DATABASE_URL, schema ownership, and lock contention")
        raise SystemExit(1) from None
