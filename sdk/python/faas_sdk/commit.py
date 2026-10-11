"""Insert outbox events using an existing customer PostgreSQL transaction."""

import json
import math
from fractions import Fraction
from typing import Any, NotRequired, Protocol, TypedDict
from uuid import UUID, uuid4


class CommitEventRouting(TypedDict):
    version: int
    key: str | int | float | bool
    platform_tenant_id: NotRequired[str]


class CommitCursor(Protocol):
    def execute(self, query: str, params: tuple[Any, ...]) -> Any: ...


def insert_commit_event(
    cursor: CommitCursor,
    event_type: str,
    data: Any,
    *,
    event_id: str | None = None,
    routing: CommitEventRouting | None = None,
) -> str:
    """Insert only; caller owns commit/rollback and must disable autocommit.

    Pass a psycopg-compatible cursor in the same transaction as the business
    write. This helper never commits, installs a schema, or contacts Gregale.
    """
    identity = str(UUID(event_id)) if event_id is not None else str(uuid4())
    if not isinstance(event_type, str) or not 1 <= len(event_type) <= 256:
        raise ValueError("Commit event type must contain 1-256 characters")
    payload = json.dumps(data, allow_nan=False, separators=(",", ":"))
    if routing is not None:
        key = routing.get("key")
        if (
            type(routing.get("version")) is not int
            or routing.get("version") != 2
            or not isinstance(key, (str, int, float, bool))
        ):
            raise ValueError("Commit routing requires version 2 and a scalar key")
        if isinstance(key, str) and (not key or len(key.encode("utf-8")) > 254):
            raise ValueError("Commit routing key must be nonempty and bounded")
        if isinstance(key, float) and not math.isfinite(key):
            raise ValueError("Commit routing key must be finite")
        if isinstance(key, (int, float)) and not isinstance(key, bool):
            encoded_key = json.dumps(key, allow_nan=False)
            if len(encoded_key) > 256:
                raise ValueError("Commit routing key must be bounded")
            parts = encoded_key.lower().split("e")
            if len(parts) == 2 and not -256 <= int(parts[1]) <= 256:
                raise ValueError("Commit routing key exponent is too large")
            exact = Fraction(encoded_key)
            canonical = str(exact.numerator) if exact.denominator == 1 else f"{exact.numerator}/{exact.denominator}"
            if len(canonical) + 2 > 256:
                raise ValueError("Commit routing key must be bounded")
        if set(routing) - {"version", "key", "platform_tenant_id"}:
            raise ValueError("Commit routing contains unknown fields")
        normalized = dict(routing)
        if "platform_tenant_id" in normalized:
            normalized["platform_tenant_id"] = str(UUID(normalized["platform_tenant_id"]))
        cursor.execute(
            "INSERT INTO public.gregale_outbox(event_id,event_type,payload,routing) VALUES (%s::uuid,%s,%s::jsonb,%s::jsonb)",
            (identity, event_type, payload, json.dumps(normalized, allow_nan=False, separators=(",", ":"))),
        )
        return identity
    cursor.execute(
        "INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (%s::uuid,%s,%s::jsonb)",
        (identity, event_type, payload),
    )
    return identity
