"""Insert outbox events using an existing customer PostgreSQL transaction."""

import json
import math
import re
from collections.abc import Awaitable
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
    schema_version: str | None = None,
) -> str:
    """Insert only inside an existing transaction; never commit or publish.

    Pass a psycopg-compatible cursor in the same transaction as the business
    write, including with_event_consumer_transaction's callback cursor. A cursor
    executing in autocommit outside a transaction is unsupported. This helper
    never commits, installs a schema, or contacts Gregale.
    """
    query, params, identity = _commit_insert(
        event_type, data, event_id=event_id, routing=routing, schema_version=schema_version
    )
    cursor.execute(query, params)
    return identity


class AsyncCommitCursor(Protocol):
    def execute(self, query: str, params: tuple[Any, ...]) -> Awaitable[Any]: ...


async def ainsert_commit_event(
    cursor: AsyncCommitCursor,
    event_type: str,
    data: Any,
    *,
    event_id: str | None = None,
    routing: CommitEventRouting | None = None,
    schema_version: str | None = None,
) -> str:
    """Insert using an existing async business cursor; never commit or publish.

    Use the cursor supplied by awith_event_consumer_transaction, or another
    live transaction. An autocommit cursor outside a transaction is unsupported.
    Validation and routing match insert_commit_event. Await errors so the owning
    transaction rolls back business writes, the outbox insert and its receipt.
    """
    query, params, identity = _commit_insert(
        event_type, data, event_id=event_id, routing=routing, schema_version=schema_version
    )
    await cursor.execute(query, params)
    return identity


def _commit_insert(
    event_type: str,
    data: Any,
    *,
    event_id: str | None,
    routing: CommitEventRouting | None,
    schema_version: str | None,
) -> tuple[str, tuple[Any, ...], str]:
    identity = str(UUID(event_id)) if event_id is not None else str(uuid4())
    if not isinstance(event_type, str) or not 1 <= len(event_type) <= 256:
        raise ValueError("Commit event type must contain 1-256 characters")
    payload = json.dumps(data, allow_nan=False, separators=(",", ":"))
    if schema_version is not None:
        if (
            not isinstance(schema_version, str)
            or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", schema_version)
            or routing is not None
        ):
            raise ValueError("Commit schema version requires an event publication without operation routing")
        return (
            "INSERT INTO public.gregale_outbox(event_id,event_type,payload,schema_version) VALUES (%s::uuid,%s,%s::jsonb,%s)",
            (identity, event_type, payload, schema_version),
            identity,
        )
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
        return (
            "INSERT INTO public.gregale_outbox(event_id,event_type,payload,routing) VALUES (%s::uuid,%s,%s::jsonb,%s::jsonb)",
            (identity, event_type, payload, json.dumps(normalized, allow_nan=False, separators=(",", ":"))),
            identity,
        )
    return (
        "INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (%s::uuid,%s,%s::jsonb)",
        (identity, event_type, payload),
        identity,
    )
