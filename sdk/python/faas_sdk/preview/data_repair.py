"""Explicit capture inside the existing PostgreSQL Operation transaction wrapper."""
from __future__ import annotations

import json
from collections.abc import Awaitable, Callable
from typing import Any, TypeVar

from .data_repair_limits import CONTEXT_MAX_BYTES, TABLE_MAX_BYTES
from .operations import CustomerOperationRequest, OperationRequest, _normalize

T = TypeVar("T")


def _params(request: OperationRequest | CustomerOperationRequest, table: str, environment: str, release: str) -> tuple[str, str, str, str]:
    if isinstance(request, CustomerOperationRequest):
        request = request._request
    request = _normalize(request)
    if not all(isinstance(v, str) and 0 < len(v.encode("utf-8")) <= limit and "\0" not in v
               for v, limit in ((table, TABLE_MAX_BYTES), (environment, CONTEXT_MAX_BYTES), (release, CONTEXT_MAX_BYTES))):
        raise ValueError("invalid data repair capture configuration")
    scope = {"account_id": request.account_id, "app_id": request.app_id, "environment": environment}
    if request.platform_tenant_id:
        scope["platform_tenant_id"] = request.platform_tenant_id
    return request.operation_id, json.dumps(scope), release, table


def with_data_repair_capture(cursor: Any, request: OperationRequest | CustomerOperationRequest, handler: Callable[[Any], T], *, table: str, environment: str, release: str) -> T:
    """Use inside a transaction callback; propagate errors to its wrapper.

    This helper performs no DDL and does not commit or authorize external callers.
    """
    params = _params(request, table, environment, release)
    cursor.execute("SELECT gregale_repair.begin_operation(%s::uuid,%s::jsonb,%s,%s::regclass)", params)
    result = handler(cursor)
    cursor.execute("SELECT gregale_repair.finish_operation(%s::uuid)", (params[0],))
    return result


async def awith_data_repair_capture(cursor: Any, request: OperationRequest | CustomerOperationRequest, handler: Callable[[Any], Awaitable[T]], *, table: str, environment: str, release: str) -> T:
    """Async equivalent for an exclusively leased transaction callback."""
    params = _params(request, table, environment, release)
    await cursor.execute("SELECT gregale_repair.begin_operation(%s::uuid,%s::jsonb,%s,%s::regclass)", params)
    result = await handler(cursor)
    await cursor.execute("SELECT gregale_repair.finish_operation(%s::uuid)", (params[0],))
    return result
