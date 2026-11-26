"""Typed application reads with signed continuation and current authorization."""

from __future__ import annotations

import asyncio
import base64
import copy
import hashlib
import hmac
import json
import re
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import asdict, dataclass
from typing import Any, Generic, Literal, TypedDict, TypeVar

from ..job_runtime import JobContract, JobError, _scope, job_input_id, job_value_hash
from .pipeline_state import PipelineScope

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
T = TypeVar("T")
QueryCode = Literal[
    "invalid_input",
    "invalid_cursor",
    "invalid_output",
    "unauthorized",
    "deadline",
    "cancelled",
    "limit",
    "query_failed",
    "not_found",
]


class QueryToolError(Exception):
    def __init__(self, code: QueryCode):
        super().__init__("SDK query stopped: " + code)
        self.code = code


@dataclass(frozen=True, slots=True)
class QueryActor:
    subject_id: str
    owner_id: str


class QueryPage(TypedDict, Generic[O]):
    items: list[O]
    next_cursor: str | None


class QueryLoadPage(TypedDict, Generic[O]):
    items: list[O]
    after: str | None


@dataclass(frozen=True, slots=True)
class QueryLoadContext:
    actor: QueryActor
    after: str | None
    limit: int
    check_cancelled: Callable[[], None]


@dataclass(frozen=True, slots=True)
class QueryAuthorizedLoadContext(QueryLoadContext):
    remaining: Callable[[], float]


def _json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"))


def _bound(value: Any, maximum: int) -> bool:
    return type(value) is int and 0 < value <= maximum


def _actor(value: QueryActor | None) -> QueryActor:
    if not isinstance(value, QueryActor) or not job_input_id(value.subject_id) or not job_input_id(value.owner_id):
        raise QueryToolError("unauthorized")
    return value


def _position(value: Any) -> bool:
    try:
        return isinstance(value, str) and 0 < len(value.encode("utf-8")) <= 2048
    except UnicodeError:
        return False


def _identity(name: str, description: str, revision: str, scope: PipelineScope) -> PipelineScope:
    try:
        if (
            not isinstance(name, str)
            or not re.fullmatch(r"[a-z][a-z0-9_]{0,47}", name)
            or not isinstance(description, str)
            or not 0 < len(description.encode()) <= 2048
            or not job_input_id(revision)
        ):
            raise ValueError
        return PipelineScope.from_dict(scope.to_dict())
    except Exception:
        raise QueryToolError("invalid_input") from None


async def _query_scope(timeout: float, work: Callable[[Callable[[], None]], Awaitable[T]]) -> T:
    _timeout(timeout)
    try:
        return await _scope(timeout, work)
    except QueryToolError:
        raise
    except JobError as e:
        if e.code == "invalid_input":
            raise QueryToolError("invalid_input") from None
        if e.code == "invalid_output":
            raise QueryToolError("invalid_output") from None
        if e.code == "deadline":
            raise QueryToolError("deadline") from None
        if e.code == "limit":
            raise QueryToolError("limit") from None
        raise QueryToolError("query_failed") from None
    except ValueError:
        # The public timeout is validated separately; callback ValueErrors are private failures.
        raise QueryToolError("query_failed") from None
    except Exception:
        raise QueryToolError("query_failed") from None


def _timeout(value: float) -> None:
    import math

    if type(value) not in (int, float) or not math.isfinite(value) or not 0 < value <= 2147483.647:
        raise QueryToolError("invalid_input")


class QueryTool(Generic[I, O, A]):
    def __init__(
        self,
        contract: JobContract[I, O],
        *,
        name: str,
        description: str,
        revision: str,
        scope: PipelineScope,
        cursor_key: str,
        authorize: Callable[[I, A, Callable[[], None]], Awaitable[QueryActor | None]],
        load: Callable[[I, QueryLoadContext], Awaitable[QueryLoadPage[O]]] | None = None,
        load_authorized: Callable[[I, QueryAuthorizedLoadContext, A], Awaitable[QueryLoadPage[O]]] | None = None,
        allow_empty_pages: bool = False,
        max_page_size: int = 100,
        max_bytes: int = 262144,
    ):
        self._scope = _identity(name, description, revision, scope)
        if (
            not isinstance(cursor_key, str)
            or not re.fullmatch(r"[\x21-\x7e]{32,128}", cursor_key)
            or not _bound(max_page_size, 100)
            or not _bound(max_bytes, 1048576)
            or not callable(authorize)
            or (load is None) == (load_authorized is None)
            or load is not None
            and not callable(load)
            or load_authorized is not None
            and not callable(load_authorized)
        ):
            raise QueryToolError("invalid_input")
        self._contract: JobContract[I, O] = JobContract(contract.spec)
        self._name, self._description, self._revision = name, description, revision
        self._key, self._authorize, self._load = cursor_key.encode("ascii"), authorize, load
        self._max_page_size, self._max_bytes = max_page_size, max_bytes
        self._load_authorized, self._allow_empty_pages = load_authorized, allow_empty_pages

    @property
    def contract(self) -> JobContract[I, O]:
        return self._contract

    @property
    def name(self) -> str:
        return self._name

    @property
    def description(self) -> str:
        return self._description

    @property
    def max_page_size(self) -> int:
        return self._max_page_size

    def _sign(self, payload: str) -> str:
        return (
            base64.urlsafe_b64encode(hmac.new(self._key, payload.encode("ascii"), hashlib.sha256).digest())
            .decode()
            .rstrip("=")
        )

    def _decode(self, cursor: str | None, binding: str) -> str | None:
        if cursor is None:
            return None
        try:
            if (
                not isinstance(cursor, str)
                or len(cursor) > 8192
                or not re.fullmatch(r"[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}", cursor)
            ):
                raise ValueError
            payload, signature = cursor.split(".")
            if not hmac.compare_digest(signature, self._sign(payload)):
                raise ValueError
            raw = base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4))
            if base64.urlsafe_b64encode(raw).decode().rstrip("=") != payload:
                raise ValueError
            p = json.loads(raw.decode("utf-8"))
            if (
                set(p) != {"a", "b", "v"}
                or type(p["v"]) is not int
                or p["v"] != 1
                or p["b"] != binding
                or not _position(p["a"])
            ):
                raise ValueError
            return str(p["a"])
        except Exception:
            raise QueryToolError("invalid_cursor") from None

    async def page(
        self,
        input: I,
        access: A,
        *,
        timeout: float,
        after: str | None = None,
        page_size: int | None = None,
    ) -> QueryPage[O]:
        _timeout(timeout)
        end = asyncio.get_running_loop().time() + timeout

        async def work(check: Callable[[], None]) -> QueryPage[O]:
            def remaining() -> float:
                check()
                left = end - asyncio.get_running_loop().time()
                if left <= 0:
                    raise QueryToolError("deadline")
                return left

            value = self._contract.input(input)
            size = min(20, self._max_page_size) if page_size is None else page_size
            if len(_json(value).encode()) > 8192 or not _bound(size, self._max_page_size):
                raise QueryToolError("invalid_input")
            first = _actor(await self._authorize(copy.deepcopy(value), access, check))
            check()
            binding = job_value_hash(
                [
                    "gregale.query.v1",
                    self._scope.to_dict(),
                    self._name,
                    self._revision,
                    self._contract.sha256,
                    value,
                    asdict(first),
                ]
            )
            position = self._decode(after, binding)
            if self._load_authorized is not None:
                context = QueryAuthorizedLoadContext(first, position, size, check, remaining)
                raw = await self._load_authorized(copy.deepcopy(value), context, access)
            else:
                assert self._load is not None
                raw = await self._load(copy.deepcopy(value), QueryLoadContext(first, position, size, check))
            check()
            if (
                not isinstance(raw, dict)
                or set(raw) != {"after", "items"}
                or not isinstance(raw["items"], list)
                or len(raw["items"]) > size
                or raw["after"] is not None
                and (
                    not _position(raw["after"])
                    or raw["after"] == position
                    or not self._allow_empty_pages
                    and not raw["items"]
                )
            ):
                raise QueryToolError("invalid_output")
            items = [self._contract.output(item) for item in raw["items"]]
            payload = (
                None
                if raw["after"] is None
                else base64.urlsafe_b64encode(_json(dict(v=1, b=binding, a=raw["after"])).encode()).decode().rstrip("=")
            )
            result = QueryPage(
                items=items, next_cursor=None if payload is None else payload + "." + self._sign(payload)
            )
            if len(_json(result).encode()) > self._max_bytes:
                raise QueryToolError("limit")
            current = _actor(await self._authorize(copy.deepcopy(value), access, check))
            check()
            if current != first:
                raise QueryToolError("unauthorized")
            return result

        return await _query_scope(timeout, work)

    async def pages(
        self,
        input: I,
        access: A,
        *,
        timeout: float,
        after: str | None = None,
        page_size: int | None = None,
        max_pages: int = 10,
        max_items: int = 1000,
    ) -> AsyncIterator[QueryPage[O]]:
        _timeout(timeout)
        size = min(20, self._max_page_size) if page_size is None else page_size
        if not _bound(max_pages, 1000) or not _bound(max_items, 100000) or not _bound(size, self._max_page_size):
            raise QueryToolError("invalid_input")
        value = self._contract.input(input)
        end, total, seen = asyncio.get_running_loop().time() + timeout, 0, set()
        for _ in range(max_pages):
            if total >= max_items:
                return
            left = end - asyncio.get_running_loop().time()
            if left <= 0:
                raise QueryToolError("deadline")
            page = await self.page(value, access, timeout=left, after=after, page_size=min(size, max_items - total))
            if page["next_cursor"] is not None:
                if page["next_cursor"] in seen:
                    raise QueryToolError("invalid_output")
                seen.add(page["next_cursor"])
            total += len(page["items"])
            yield page
            if page["next_cursor"] is None:
                return
            after = page["next_cursor"]

    async def items(
        self,
        input: I,
        access: A,
        *,
        timeout: float,
        after: str | None = None,
        page_size: int | None = None,
        max_pages: int = 10,
        max_items: int = 1000,
    ) -> AsyncIterator[O]:
        _timeout(timeout)
        end = asyncio.get_running_loop().time() + timeout
        async for page in self.pages(
            input, access, timeout=timeout, after=after, page_size=page_size, max_pages=max_pages, max_items=max_items
        ):
            for item in page["items"]:
                if asyncio.get_running_loop().time() >= end:
                    raise QueryToolError("deadline")
                yield item


class QueryResourceLink(TypedDict):
    type: Literal["resource_link"]
    uri: str
    name: str
    description: str
    mimeType: str


class QueryResourceContent(TypedDict):
    uri: str
    mimeType: str
    text: str


class QueryResource(Generic[A]):
    def __init__(
        self,
        *,
        name: str,
        description: str,
        revision: str,
        scope: PipelineScope,
        mime_type: str,
        authorize: Callable[[str, A, Callable[[], None]], Awaitable[QueryActor | None]],
        read: Callable[[str, QueryActor, Callable[[], None]], Awaitable[str]],
        max_bytes: int = 262144,
    ):
        scope = _identity(name, description, revision, scope)
        if (
            not isinstance(mime_type, str)
            or not re.fullmatch(r"[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+", mime_type)
            or not _bound(max_bytes, 1048576)
            or not callable(authorize)
            or not callable(read)
        ):
            raise QueryToolError("invalid_input")
        self._name, self._description, self._mime, self._max_bytes = name, description, mime_type, max_bytes
        self._authorize, self._read = authorize, read
        self._prefix = (
            "gregale-query://"
            + job_value_hash(["gregale.query-resource.v1", scope.to_dict(), name, revision])
            + "/"
            + name
            + "/"
        )

    @property
    def name(self) -> str:
        return self._name

    @property
    def template(self) -> dict[str, str]:
        return dict(
            uriTemplate=self._prefix + "{id}", name=self._name, description=self._description, mimeType=self._mime
        )

    @staticmethod
    def _valid_id(id: Any) -> bool:
        return isinstance(id, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,128}", id) is not None

    async def link(self, id: str, access: A, *, timeout: float) -> QueryResourceLink:
        _timeout(timeout)

        async def work(check: Callable[[], None]) -> QueryResourceLink:
            if not self._valid_id(id):
                raise QueryToolError("invalid_input")
            _actor(await self._authorize(id, access, check))
            check()
            return QueryResourceLink(
                type="resource_link",
                uri=self._prefix + id,
                name=self._name + "/" + id,
                description=self._description,
                mimeType=self._mime,
            )

        return await _query_scope(timeout, work)

    async def read(self, uri: str, access: A, *, timeout: float) -> QueryResourceContent:
        _timeout(timeout)

        async def work(check: Callable[[], None]) -> QueryResourceContent:
            if (
                not isinstance(uri, str)
                or not uri.startswith(self._prefix)
                or not self._valid_id(uri[len(self._prefix) :])
            ):
                raise QueryToolError("not_found")
            id = uri[len(self._prefix) :]
            first = _actor(await self._authorize(id, access, check))
            check()
            text = await self._read(id, first, check)
            check()
            if not isinstance(text, str):
                raise QueryToolError("invalid_output")
            if len(text.encode()) > self._max_bytes:
                raise QueryToolError("limit")
            current = _actor(await self._authorize(id, access, check))
            check()
            if current != first:
                raise QueryToolError("unauthorized")
            return QueryResourceContent(uri=uri, mimeType=self._mime, text=text)

        return await _query_scope(timeout, work)
