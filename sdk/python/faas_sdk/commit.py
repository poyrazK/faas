"""Insert outbox events using an existing customer PostgreSQL transaction."""

import json
from typing import Any, Protocol
from uuid import UUID, uuid4


class CommitCursor(Protocol):
    def execute(self, query: str, params: tuple[Any, ...]) -> Any: ...


def insert_commit_event(cursor: CommitCursor, event_type: str, data: Any, *, event_id: str | None = None) -> str:
    """Insert only; caller owns commit/rollback and must disable autocommit.

    Pass a psycopg-compatible cursor in the same transaction as the business
    write. This helper never commits, installs a schema, or contacts Gregale.
    """
    identity = str(UUID(event_id)) if event_id is not None else str(uuid4())
    if not isinstance(event_type, str) or not 1 <= len(event_type) <= 256:
        raise ValueError("Commit event type must contain 1-256 characters")
    payload = json.dumps(data, allow_nan=False, separators=(",", ":"))
    cursor.execute(
        "INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (%s::uuid,%s,%s::jsonb)",
        (identity, event_type, payload),
    )
    return identity
