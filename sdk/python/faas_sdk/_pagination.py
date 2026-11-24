"""Lazy, typed traversal of public deployment and build collections."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncGenerator, AsyncIterator, Callable, Iterator
from contextlib import suppress
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Generic, Literal, TypeVar

from ._async_request import (
    AsyncRequestTransport,
    RequestOptions,
    RequestTimeoutError,
    run_request,
    snapshot_async_client,
)
from .types import UNSET, Unset

if TYPE_CHECKING:
    import httpx

    from ._wrapper import FaaSClient
    from .models.build_response import BuildResponse
    from .models.deployment_response import DeploymentResponse
    from .models.get_builds_status import GetBuildsStatus

T = TypeVar("T")
V = TypeVar("V")


@dataclass(frozen=True, slots=True)
class PaginationOptions(RequestOptions):
    """One traversal budget, starting at first consumption, including consumer pauses.

    Timeout is in seconds (None inherits the client, zero disables it). Counts
    are nonnegative; zero performs no HTTP. Cancellation uses asyncio tasks or
    AsyncPagination.aclose(), including while anext() is pending.
    """

    max_pages: int | None = None
    max_items: int | None = None

    def __post_init__(self) -> None:
        RequestOptions.__post_init__(self)
        for name in ("max_pages", "max_items"):
            value = getattr(self, name)
            if value is not None and (type(value) is not int or not 0 <= value <= 2**53 - 1):
                raise ValueError(f"{name} must be a nonnegative safe integer")


@dataclass(frozen=True, slots=True)
class PaginationPage(Generic[T]):
    """Typed rows and the EXACT server cursor. Never converted to a datetime.

    Save next_before to resume after the entire page has been processed.
    Generated legacy list-response models remain unchanged.
    """

    items: tuple[T, ...]
    next_before: str | None


class PaginationError(ValueError):
    """An invalid envelope or cursor cycle; messages omit response/cursor data."""

    def __init__(self, code: Literal["invalid_page", "repeated_cursor"]) -> None:
        self.code = code
        super().__init__("Invalid pagination page" if code == "invalid_page" else "Repeated pagination cursor")


class AsyncPagination(AsyncIterator[T]):
    """Single-consumer lazy iterator. Use async with for deterministic early cleanup.

    aclose() also interrupts a pending anext(), unlike a native async generator's
    aclose(). Closing this iterator never closes the owning SDK client.
    """

    def __init__(self, driver: AsyncGenerator[T, None], client: httpx.AsyncClient) -> None:
        self._driver = driver
        self._client = client
        self._pending: asyncio.Task[T] | None = None
        self._closed = False

    def __aiter__(self) -> AsyncPagination[T]:
        return self

    async def __anext__(self) -> T:
        if self._closed:
            raise StopAsyncIteration
        if self._pending is not None:
            raise RuntimeError("pagination supports one pending anext()")
        task = asyncio.create_task(self._driver.__anext__())
        self._pending = task
        try:
            result = await asyncio.shield(task)
            if self._closed:
                raise asyncio.CancelledError()
            return result
        except BaseException:
            await self.aclose()
            raise
        finally:
            self._pending = None

    async def aclose(self) -> None:
        if self._closed:
            return
        self._closed = True
        if self._pending is not None:
            self._pending.cancel()
            with suppress(Exception, asyncio.CancelledError):
                await self._pending
        try:
            await self._driver.aclose()
        finally:
            await self._client.aclose()

    async def __aenter__(self) -> AsyncPagination[T]:
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        await self.aclose()


def _paginate(
    owner: FaaSClient,
    *,
    kwargs: Callable[[int, str | Unset], dict[str, Any]],
    parse: Callable[[dict[str, Any]], T],
    select: Callable[[PaginationPage[T]], Iterator[V]],
    limit: int,
    before: str | None,
    options: PaginationOptions | None,
) -> AsyncPagination[V]:
    if type(limit) is not int or not 1 <= limit <= 200:
        raise ValueError("limit must be an integer from 1 through 200")
    if before is not None and not isinstance(before, str):
        raise TypeError("before must be an opaque string or None")
    policy = options or PaginationOptions()
    if not isinstance(policy, PaginationOptions):
        raise TypeError("options must be PaginationOptions")
    source = owner.async_httpx_client
    transport = source._transport
    if not isinstance(transport, AsyncRequestTransport):
        raise TypeError("pagination requires the SDK async transport chain")
    budget = owner._request_timeout if policy.timeout is None else policy.timeout
    # Capture configuration now; token/base URL rotations apply to new traversals.
    client = snapshot_async_client(source)

    async def drive() -> AsyncGenerator[V, None]:
        loop = asyncio.get_running_loop()
        deadline = loop.time() + budget if budget else None

        def remaining() -> float:
            if deadline is None:
                return 0
            left = deadline - loop.time()
            if left <= 0:
                raise RequestTimeoutError("SDK pagination deadline exceeded")
            return left

        cursor = before
        seen = {cursor} if cursor else set()
        pages = items = 0
        while (policy.max_pages is None or pages < policy.max_pages) and (
            policy.max_items is None or items < policy.max_items
        ):
            size = limit if policy.max_items is None else min(limit, policy.max_items - items)

            async def fetch() -> PaginationPage[T]:
                response = await client.request(**kwargs(size, cursor if cursor is not None else UNSET))
                response.raise_for_status()
                try:
                    body = response.json()
                    if not isinstance(body, dict) or not isinstance(body.get("items"), list):
                        raise ValueError
                    next_before = body.get("next_before")
                    if next_before is not None and not isinstance(next_before, str):
                        raise ValueError
                    if len(body["items"]) > size or any(not isinstance(row, dict) for row in body["items"]):
                        raise ValueError
                    return PaginationPage(tuple(parse(row) for row in body["items"]), next_before)
                except (ValueError, TypeError, KeyError, AttributeError, OverflowError):
                    raise PaginationError("invalid_page") from None

            page = await run_request(
                transport.owner,
                fetch,
                RequestOptions(timeout=remaining(), retry=policy.retry, on_response=policy.on_response),
                budget,
            )
            next_cursor = page.next_before
            if next_cursor:
                if next_cursor in seen:
                    raise PaginationError("repeated_cursor")
                seen.add(next_cursor)
            pages += 1
            items += len(page.items)
            for value in select(page):
                remaining()
                yield value
            if not next_cursor:
                return
            cursor = next_cursor

    return AsyncPagination(drive(), client)


def _deployments(
    owner: FaaSClient,
    slug: str | None,
    select: Callable[[PaginationPage[DeploymentResponse]], Iterator[V]],
    *,
    limit: int,
    before: str | None,
    options: PaginationOptions | None,
) -> AsyncPagination[V]:
    from .api.deployments import list_app_deployments, list_deployments
    from .models.deployment_response import DeploymentResponse

    def kwargs(size: int, cursor: str | Unset) -> dict[str, Any]:
        if slug is None:
            return list_deployments._get_kwargs(limit=size, before=cursor)
        return list_app_deployments._get_kwargs(slug, limit=size, before=cursor)

    return _paginate(
        owner,
        kwargs=kwargs,
        parse=DeploymentResponse.from_dict,
        select=select,
        limit=limit,
        before=before,
        options=options,
    )


def _builds(
    owner: FaaSClient,
    select: Callable[[PaginationPage[BuildResponse]], Iterator[V]],
    *,
    app: str | Unset,
    status: GetBuildsStatus | Unset,
    limit: int,
    before: str | None,
    options: PaginationOptions | None,
) -> AsyncPagination[V]:
    from .api.deployments import get_builds
    from .models.build_response import BuildResponse
    from .models.get_builds_status import check_get_builds_status

    status = status if isinstance(status, Unset) else check_get_builds_status(status)
    return _paginate(
        owner,
        kwargs=lambda size, cursor: get_builds._get_kwargs(app=app, status=status, limit=size, before=cursor),
        parse=BuildResponse.from_dict,
        select=select,
        limit=limit,
        before=before,
        options=options,
    )
