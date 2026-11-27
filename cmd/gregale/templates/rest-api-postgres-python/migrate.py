"""Release-only schema application; never used by serving startup."""

import json
import os
from pathlib import Path

import psycopg
from database import connection_kwargs


def seed_sandbox_notes(connection):
    seed = os.environ.get("GREGALE_API_SANDBOX_SEED")
    if seed is None:
        connection.execute(
            """INSERT INTO public.notes (body)
               SELECT %s WHERE NOT EXISTS (SELECT 1 FROM public.notes)""",
            ("Sample note from the API sandbox",),
        )
        return

    definition = json.loads(seed)
    notes = definition.get("notes", [])
    if not isinstance(notes, list):
        raise ValueError("Sandbox seed notes must be an array")

    seen = set()
    for note in notes:
        note_id = note.get("id") if isinstance(note, dict) else None
        body = note.get("body") if isinstance(note, dict) else None
        if (type(note_id) is not int or not 1 <= note_id < 2147483647 or note_id in seen
                or not isinstance(body, str) or not body or "\x00" in body):
            raise ValueError("Sandbox notes require unique positive integer IDs and nonempty bodies")
        seen.add(note_id)
        connection.execute(
            """INSERT INTO public.notes (id, body) VALUES (%s, %s)
               ON CONFLICT (id) DO NOTHING""",
            (note_id, body),
        )

    if notes:
        connection.execute(
            """SELECT setval(pg_get_serial_sequence('public.notes', 'id'),
                       GREATEST((SELECT last_value FROM public.notes_id_seq),
                                (SELECT max(id) FROM public.notes)), true)"""
        )


def migrate(uri):
    if not uri:
        raise ValueError("Migration credentials are required")
    with psycopg.connect(uri, **connection_kwargs(uri)) as connection:
        connection.execute("SET statement_timeout = '120s'")
        connection.execute("SET lock_timeout = '30s'")
        with connection.transaction():
            connection.execute("SELECT pg_advisory_xact_lock(734928146)")
            connection.execute(Path(__file__).with_name("schema.sql").read_text())
            if os.environ.get("GREGALE_API_SANDBOX") == "1":
                seed_sandbox_notes(connection)


if __name__ == "__main__":
    try:
        migrate(os.environ.get("MIGRATION_DATABASE_URL"))
        print("Notes schema ready")
    except Exception:
        print("Migration failed: check migration credentials, schema ownership, locks, and any sandbox seed")
        raise SystemExit(1) from None
