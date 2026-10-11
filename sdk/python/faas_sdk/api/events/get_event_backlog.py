from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_backlog_response import EventBacklogResponse
from ...models.get_event_backlog_capacity_scope import (
    GetEventBacklogCapacityScope,
)
from ...models.get_event_backlog_state import GetEventBacklogState
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app: str | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    state: GetEventBacklogState | Unset = UNSET,
    capacity_scope: GetEventBacklogCapacityScope | Unset = UNSET,
    min_age_seconds: int | Unset = 0,
    after: str | Unset = UNSET,
    consumers_after: str | Unset = UNSET,
    limit: int | Unset = 100,
    consumer_limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["app"] = app

    params["subscription_id"] = subscription_id

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    json_capacity_scope: str | Unset = UNSET
    if not isinstance(capacity_scope, Unset):
        json_capacity_scope = capacity_scope

    params["capacity_scope"] = json_capacity_scope

    params["min_age_seconds"] = min_age_seconds

    params["after"] = after

    params["consumers_after"] = consumers_after

    params["limit"] = limit

    params["consumer_limit"] = consumer_limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/events/backlog",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventBacklogResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventBacklogResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventBacklogResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    app: str | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    state: GetEventBacklogState | Unset = UNSET,
    capacity_scope: GetEventBacklogCapacityScope | Unset = UNSET,
    min_age_seconds: int | Unset = 0,
    after: str | Unset = UNSET,
    consumers_after: str | Unset = UNSET,
    limit: int | Unset = 100,
    consumer_limit: int | Unset = 100,
) -> Response[EventBacklogResponse | Problem]:
    """Discover waiting application event recipients and consumer counts.

     Requires apps:read or admin. Reads only the authenticated account's
    captured application recipients in pending or processing routing state,
    in both whole-event and independent-recipient routing modes. Includes
    capacity waits before an invocation exists; excludes settled routing and
    handler execution queues. Returns metadata and receipt/history links,
    never envelope data. Consumers count all matching recipients, independently
    of either bounded page. Age is measured from durable event acceptance.
    Recipient pages are oldest accepted first, then receipt and subscription
    identity; consumer pages use app and subscription identity. Pass each
    continuation cursor with the same filters; page sizes may change. Cursors
    anchor window_at and its age cutoff, while membership and counts remain
    live on every request. Recovered rows disappear, including cursor rows.
    Replay behind a cursor requires restarting discovery. This inspection
    ordering does not guarantee delivery FIFO. A repeatable read keeps each
    response consistent. Aggregation has a five-second deadline; narrow
    filters if event_backlog_read_timeout is returned. Responses use
    Cache-Control no-store. unattributed_receipts counts pending legacy
    receipts without captured recipients across the account; only the
    acceptance/age window applies to that count, including with other filters.

    Args:
        app (str | Unset):
        subscription_id (str | Unset):
        state (GetEventBacklogState | Unset):
        capacity_scope (GetEventBacklogCapacityScope | Unset):
        min_age_seconds (int | Unset):  Default: 0.
        after (str | Unset):
        consumers_after (str | Unset):
        limit (int | Unset):  Default: 100.
        consumer_limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventBacklogResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
        subscription_id=subscription_id,
        state=state,
        capacity_scope=capacity_scope,
        min_age_seconds=min_age_seconds,
        after=after,
        consumers_after=consumers_after,
        limit=limit,
        consumer_limit=consumer_limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    app: str | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    state: GetEventBacklogState | Unset = UNSET,
    capacity_scope: GetEventBacklogCapacityScope | Unset = UNSET,
    min_age_seconds: int | Unset = 0,
    after: str | Unset = UNSET,
    consumers_after: str | Unset = UNSET,
    limit: int | Unset = 100,
    consumer_limit: int | Unset = 100,
) -> EventBacklogResponse | Problem | None:
    """Discover waiting application event recipients and consumer counts.

     Requires apps:read or admin. Reads only the authenticated account's
    captured application recipients in pending or processing routing state,
    in both whole-event and independent-recipient routing modes. Includes
    capacity waits before an invocation exists; excludes settled routing and
    handler execution queues. Returns metadata and receipt/history links,
    never envelope data. Consumers count all matching recipients, independently
    of either bounded page. Age is measured from durable event acceptance.
    Recipient pages are oldest accepted first, then receipt and subscription
    identity; consumer pages use app and subscription identity. Pass each
    continuation cursor with the same filters; page sizes may change. Cursors
    anchor window_at and its age cutoff, while membership and counts remain
    live on every request. Recovered rows disappear, including cursor rows.
    Replay behind a cursor requires restarting discovery. This inspection
    ordering does not guarantee delivery FIFO. A repeatable read keeps each
    response consistent. Aggregation has a five-second deadline; narrow
    filters if event_backlog_read_timeout is returned. Responses use
    Cache-Control no-store. unattributed_receipts counts pending legacy
    receipts without captured recipients across the account; only the
    acceptance/age window applies to that count, including with other filters.

    Args:
        app (str | Unset):
        subscription_id (str | Unset):
        state (GetEventBacklogState | Unset):
        capacity_scope (GetEventBacklogCapacityScope | Unset):
        min_age_seconds (int | Unset):  Default: 0.
        after (str | Unset):
        consumers_after (str | Unset):
        limit (int | Unset):  Default: 100.
        consumer_limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventBacklogResponse | Problem
    """

    return sync_detailed(
        client=client,
        app=app,
        subscription_id=subscription_id,
        state=state,
        capacity_scope=capacity_scope,
        min_age_seconds=min_age_seconds,
        after=after,
        consumers_after=consumers_after,
        limit=limit,
        consumer_limit=consumer_limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    app: str | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    state: GetEventBacklogState | Unset = UNSET,
    capacity_scope: GetEventBacklogCapacityScope | Unset = UNSET,
    min_age_seconds: int | Unset = 0,
    after: str | Unset = UNSET,
    consumers_after: str | Unset = UNSET,
    limit: int | Unset = 100,
    consumer_limit: int | Unset = 100,
) -> Response[EventBacklogResponse | Problem]:
    """Discover waiting application event recipients and consumer counts.

     Requires apps:read or admin. Reads only the authenticated account's
    captured application recipients in pending or processing routing state,
    in both whole-event and independent-recipient routing modes. Includes
    capacity waits before an invocation exists; excludes settled routing and
    handler execution queues. Returns metadata and receipt/history links,
    never envelope data. Consumers count all matching recipients, independently
    of either bounded page. Age is measured from durable event acceptance.
    Recipient pages are oldest accepted first, then receipt and subscription
    identity; consumer pages use app and subscription identity. Pass each
    continuation cursor with the same filters; page sizes may change. Cursors
    anchor window_at and its age cutoff, while membership and counts remain
    live on every request. Recovered rows disappear, including cursor rows.
    Replay behind a cursor requires restarting discovery. This inspection
    ordering does not guarantee delivery FIFO. A repeatable read keeps each
    response consistent. Aggregation has a five-second deadline; narrow
    filters if event_backlog_read_timeout is returned. Responses use
    Cache-Control no-store. unattributed_receipts counts pending legacy
    receipts without captured recipients across the account; only the
    acceptance/age window applies to that count, including with other filters.

    Args:
        app (str | Unset):
        subscription_id (str | Unset):
        state (GetEventBacklogState | Unset):
        capacity_scope (GetEventBacklogCapacityScope | Unset):
        min_age_seconds (int | Unset):  Default: 0.
        after (str | Unset):
        consumers_after (str | Unset):
        limit (int | Unset):  Default: 100.
        consumer_limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventBacklogResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
        subscription_id=subscription_id,
        state=state,
        capacity_scope=capacity_scope,
        min_age_seconds=min_age_seconds,
        after=after,
        consumers_after=consumers_after,
        limit=limit,
        consumer_limit=consumer_limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    app: str | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    state: GetEventBacklogState | Unset = UNSET,
    capacity_scope: GetEventBacklogCapacityScope | Unset = UNSET,
    min_age_seconds: int | Unset = 0,
    after: str | Unset = UNSET,
    consumers_after: str | Unset = UNSET,
    limit: int | Unset = 100,
    consumer_limit: int | Unset = 100,
) -> EventBacklogResponse | Problem | None:
    """Discover waiting application event recipients and consumer counts.

     Requires apps:read or admin. Reads only the authenticated account's
    captured application recipients in pending or processing routing state,
    in both whole-event and independent-recipient routing modes. Includes
    capacity waits before an invocation exists; excludes settled routing and
    handler execution queues. Returns metadata and receipt/history links,
    never envelope data. Consumers count all matching recipients, independently
    of either bounded page. Age is measured from durable event acceptance.
    Recipient pages are oldest accepted first, then receipt and subscription
    identity; consumer pages use app and subscription identity. Pass each
    continuation cursor with the same filters; page sizes may change. Cursors
    anchor window_at and its age cutoff, while membership and counts remain
    live on every request. Recovered rows disappear, including cursor rows.
    Replay behind a cursor requires restarting discovery. This inspection
    ordering does not guarantee delivery FIFO. A repeatable read keeps each
    response consistent. Aggregation has a five-second deadline; narrow
    filters if event_backlog_read_timeout is returned. Responses use
    Cache-Control no-store. unattributed_receipts counts pending legacy
    receipts without captured recipients across the account; only the
    acceptance/age window applies to that count, including with other filters.

    Args:
        app (str | Unset):
        subscription_id (str | Unset):
        state (GetEventBacklogState | Unset):
        capacity_scope (GetEventBacklogCapacityScope | Unset):
        min_age_seconds (int | Unset):  Default: 0.
        after (str | Unset):
        consumers_after (str | Unset):
        limit (int | Unset):  Default: 100.
        consumer_limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventBacklogResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app=app,
            subscription_id=subscription_id,
            state=state,
            capacity_scope=capacity_scope,
            min_age_seconds=min_age_seconds,
            after=after,
            consumers_after=consumers_after,
            limit=limit,
            consumer_limit=consumer_limit,
        )
    ).parsed
