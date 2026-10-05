"""ADR-586: customer PostgreSQL transactions with replayable operation responses."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from decimal import Decimal
from importlib.resources import files
from typing import Any, NotRequired, TypedDict

from ._operation_contract import (
    OPERATION_EFFECTS,
    OPERATION_IDENTITY_BYTES,
    OPERATION_PAYLOAD_BYTES,
    OPERATION_REQUEST_BYTES,
    OPERATION_RESPONSE_BYTES,
    OPERATION_TYPE_BYTES,
)

operation_receipt_schema = files(__package__).joinpath("operation_schema.sql").read_text(encoding="utf-8")
_SUPPORTED = object()


class OperationConflictError(ValueError):
    """The existing operation has another scope or HTTP input."""


class OperationCommitUnknownError(RuntimeError):
    """Retry the same operation; the previous COMMIT may have succeeded."""


@dataclass(frozen=True)
class OperationRequest:
    operation_id: str
    account_id: str
    app_id: str
    generation: int
    method: str
    path: str
    body: bytes
    platform_tenant_id: str = ""
    _support: object = field(default=None, repr=False, compare=False)


class OperationEffect(TypedDict):
    name: str
    webhook_id: str
    type: str
    payload: Any


class OperationOutcome(TypedDict):
    result: Any
    effects: NotRequired[list[OperationEffect]]


@dataclass(frozen=True)
class OperationTransactionResult:
    """Send body unchanged with Content-Type application/json after success."""

    body: bytes
    replayed: bool


def _uuid(value: str) -> str:
    if (
        not isinstance(value, str)
        or not re.fullmatch(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}", value)
        or value == "00000000-0000-0000-0000-000000000000"
    ):
        raise ValueError("operation identity must be a nonzero UUID")
    return value.lower()


def _normalize(request: OperationRequest) -> OperationRequest:
    if request._support is not _SUPPORTED:
        raise ValueError("use operation_request_from_headers with negotiated support")
    if type(request.generation) is not int or not 1 <= request.generation <= 9223372036854775807:
        raise ValueError("operation generation must be a positive int64")
    if (
        not isinstance(request.method, str)
        or not re.fullmatch(r"[A-Z]+", request.method)
        or len(request.method) > OPERATION_IDENTITY_BYTES
        or not isinstance(request.path, str)
        or not request.path.startswith("/")
        or any(char in request.path for char in "\r\n\0")
        or not isinstance(request.body, bytes)
        or len(request.method) + len(request.path.encode("utf-8")) + len(request.body) > OPERATION_REQUEST_BYTES
    ):
        raise ValueError("invalid or oversized operation request")
    return replace(
        request,
        operation_id=_uuid(request.operation_id),
        account_id=_uuid(request.account_id),
        app_id=_uuid(request.app_id),
        platform_tenant_id=_uuid(request.platform_tenant_id) if request.platform_tenant_id else "",
    )


def operation_request_from_headers(
    headers: Mapping[str, str | Sequence[str]], method: str, path: str, body: bytes
) -> OperationRequest:
    """Use only behind Gregale ingress, which strips and authors these headers."""

    def read(name: str, optional: bool = False) -> str:
        values: list[str] = []
        for key, value in headers.items():
            if key.lower() == name:
                values.extend([value] if isinstance(value, str) else value)
        if not values and optional:
            return ""
        if len(values) != 1 or not isinstance(values[0], str) or not values[0]:
            raise ValueError(f"operation requires one {name} header")
        return values[0]

    if read("x-gregale-operation-result-version") != "1":
        raise ValueError("managed operation results are not supported")
    generation = read("x-gregale-operation-generation")
    if not re.fullmatch(r"[1-9][0-9]{0,18}", generation):
        raise ValueError("operation generation must be a positive int64")
    return _normalize(
        OperationRequest(
            operation_id=read("x-gregale-operation-id"),
            account_id=read("x-faas-tenant-id"),
            app_id=read("x-faas-app-id"),
            platform_tenant_id=read("x-faas-platform-tenant-id", True),
            generation=int(generation),
            method=method,
            path=path,
            body=body,
            _support=_SUPPORTED,
        )
    )


def operation_request_digest(input: OperationRequest) -> bytes:
    request = _normalize(input)
    prefix = f"gregale-operation-request-v1\n{request.method}\n{request.path}\n".encode()
    return hashlib.sha256(prefix + request.body).digest()


def _json(value: Any) -> bytes:
    return json.dumps(value, allow_nan=False, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def _receipt_decoder() -> json.JSONDecoder:
    # Control fields need small integers; opaque payload/result numbers need no
    # float conversion or Python's integer-to-string digit limit on replay.
    return json.JSONDecoder(
        parse_int=lambda value: int(value) if len(value) < 20 else Decimal(value), parse_float=Decimal
    )


def _raw_values(source: str) -> list[tuple[str, str]]:
    # JSON has already been parsed. Preserve value spans so replay validation
    # measures wire bytes without re-encoding numbers through Python floats.
    decoder = _receipt_decoder()
    source = source.strip()
    object_values = source[0] == "{"
    index = 1
    values: list[tuple[str, str]] = []
    while index < len(source):
        while source[index].isspace():
            index += 1
        if source[index] in "}]":
            break
        key = ""
        if object_values:
            key, index = decoder.raw_decode(source, index)
            while source[index].isspace():
                index += 1
            index += 1  # colon
            while source[index].isspace():
                index += 1
        _, end = decoder.raw_decode(source, index)
        values.append((key, source[index:end]))
        index = end
        while source[index].isspace():
            index += 1
        if source[index] == ",":
            index += 1
    return values


def _validate_body(body: bytes) -> None:
    if len(body) > OPERATION_RESPONSE_BYTES:
        raise ValueError("operation response exceeds platform limit")
    value = _receipt_decoder().decode(body.decode("utf-8"))
    if (
        not isinstance(value, dict)
        or set(value) != {"gregale_operation_result", "result", "effects"}
        or type(value["gregale_operation_result"]) is not int
        or value["gregale_operation_result"] != 1
        or not isinstance(value["effects"], list)
        or len(value["effects"]) > OPERATION_EFFECTS
    ):
        raise ValueError("invalid saved operation response")
    names: set[str] = set()
    raw_effects = _raw_values(dict(_raw_values(body.decode("utf-8")))["effects"])
    for index, effect in enumerate(value["effects"]):
        if (
            not isinstance(effect, dict)
            or set(effect) != {"name", "webhook_id", "type", "payload"}
            or not isinstance(effect["name"], str)
            or not re.fullmatch(r"[a-z][a-z0-9-]{0,62}", effect["name"])
            or effect["name"] in names
            or not isinstance(effect["type"], str)
            or not re.fullmatch(r"[a-z][a-z0-9_.-]*", effect["type"])
            or len(effect["type"]) > OPERATION_TYPE_BYTES
        ):
            raise ValueError("invalid operation effect")
        payload = dict(_raw_values(raw_effects[index][1]))["payload"]
        if len(payload.encode("utf-8")) > OPERATION_PAYLOAD_BYTES:
            raise ValueError("operation payload exceeds platform limit")
        _uuid(effect["webhook_id"])
        names.add(effect["name"])


def _encode(outcome: OperationOutcome) -> bytes:
    if not isinstance(outcome, dict) or "result" not in outcome or set(outcome) - {"result", "effects"}:
        raise ValueError("operation outcome requires result and optional effects")
    body = _json({"gregale_operation_result": 1, "result": outcome["result"], "effects": outcome.get("effects", [])})
    _validate_body(body)
    return body


_LOCK = "SELECT pg_advisory_xact_lock(hashtextextended('gregale.operation-inbox.v1:' || %s::uuid::text, 0))"
_READ = "SELECT account_id::text,app_id::text,coalesce(platform_tenant_id::text,''),request_digest,response_body FROM public.gregale_operation_inbox WHERE operation_id=%s::uuid"
_INSERT = "INSERT INTO public.gregale_operation_inbox(operation_id,account_id,app_id,platform_tenant_id,request_digest,response_body) VALUES (%s::uuid,%s::uuid,%s::uuid,nullif(%s,'')::uuid,%s,%s)"


def _ready(connection: Any) -> None:
    # A psycopg-compatible connection must be autocommit/idle. Otherwise its
    # transaction manager can create a savepoint and return before outer COMMIT.
    if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
        raise ValueError("operation requires an idle autocommit connection; wrapper owns the transaction")


def _replay(row: Any, request: OperationRequest, digest: bytes) -> bytes:
    if (
        row[0] != request.account_id
        or row[1] != request.app_id
        or row[2] != request.platform_tenant_id
        or bytes(row[3]) != digest
    ):
        raise OperationConflictError("operation receipt scope or input differs")
    body = row[4].encode("utf-8")
    _validate_body(body)
    return body


def _params(request: OperationRequest, digest: bytes, body: bytes) -> tuple[Any, ...]:
    return request.operation_id, request.account_id, request.app_id, request.platform_tenant_id, digest, body.decode()


def _tuple_row(_cursor: Any) -> Callable[[Sequence[Any]], tuple[Any, ...]]:
    return tuple


def with_operation_transaction(
    connection: Any, input: OperationRequest, handler: Callable[[Any], OperationOutcome]
) -> OperationTransactionResult:
    """Own a fresh READ COMMITTED transaction; handler gets a psycopg cursor.

    Do not commit/rollback, change transaction settings, or perform external
    effects inside the callback. Errors do not automatically rerun the callback.
    """
    request = _normalize(input)
    digest = operation_request_digest(request)
    _ready(connection)
    completed = False
    try:
        with connection.transaction():
            with connection.cursor(row_factory=_tuple_row) as cursor:
                cursor.execute("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
                cursor.execute(_LOCK, (request.operation_id,))
                cursor.execute(_READ, (request.operation_id,))
                row = cursor.fetchone()
                if row is not None:
                    body = _replay(row, request, digest)
                else:
                    with connection.cursor() as business_cursor:
                        body = _encode(handler(business_cursor))
                if row is None:
                    cursor.execute(_INSERT, _params(request, digest, body))
                completed = True
    except Exception as error:
        if completed:
            raise OperationCommitUnknownError("operation commit outcome unknown; retry the same operation") from error
        raise
    return OperationTransactionResult(body, row is not None)


async def awith_operation_transaction(
    connection: Any, input: OperationRequest, handler: Callable[[Any], Awaitable[OperationOutcome]]
) -> OperationTransactionResult:
    """Async psycopg equivalent; connection must be idle and autocommit."""
    request = _normalize(input)
    digest = operation_request_digest(request)
    _ready(connection)
    completed = False
    try:
        async with connection.transaction():
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                await cursor.execute("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
                await cursor.execute(_LOCK, (request.operation_id,))
                await cursor.execute(_READ, (request.operation_id,))
                row = await cursor.fetchone()
                if row is not None:
                    body = _replay(row, request, digest)
                else:
                    async with connection.cursor() as business_cursor:
                        body = _encode(await handler(business_cursor))
                if row is None:
                    await cursor.execute(_INSERT, _params(request, digest, body))
                completed = True
    except Exception as error:
        if completed:
            raise OperationCommitUnknownError("operation commit outcome unknown; retry the same operation") from error
        raise
    return OperationTransactionResult(body, row is not None)
