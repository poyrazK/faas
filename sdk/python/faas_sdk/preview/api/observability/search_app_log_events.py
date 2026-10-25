import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.log_events_response import LogEventsResponse
from ...models.problem import Problem
from ...models.search_app_log_events_source import SearchAppLogEventsSource
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: SearchAppLogEventsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
    saved: str | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    json_source: str | Unset = UNSET
    if not isinstance(source, Unset):
        json_source = source

    params["source"] = json_source

    params["level"] = level

    params["route"] = route

    params["status"] = status

    json_deployment_id: str | Unset = UNSET
    if not isinstance(deployment_id, Unset):
        json_deployment_id = str(deployment_id)
    params["deployment_id"] = json_deployment_id

    params["request_id"] = request_id

    params["trace_id"] = trace_id

    params["q"] = q

    params["saved"] = saved

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/log-events".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> LogEventsResponse | Problem:
    if response.status_code == 200:
        response_200 = LogEventsResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[LogEventsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: SearchAppLogEventsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
    saved: str | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> Response[LogEventsResponse | Problem]:
    """Search an app's log events.

     ADR-20261018083536 internal preview, enabled by
    `FAAS_LOG_SEARCH_ENABLED=1`; otherwise 503 `log_search_unavailable`.
    Requires apps:read or admin.

    Returns one newest-first page of the app's ADR-213 log ledger. Filters
    combine with AND. A text search (`q`) covers at most 24 hours and
    answers 400 `log_search_window_too_large` beyond that; every search
    runs under a 5 second statement timeout and answers 503
    `log_search_timeout` when it trips. Pass `next_cursor` back as
    `cursor` for the following page.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (SearchAppLogEventsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):
        saved (str | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        source=source,
        level=level,
        route=route,
        status=status,
        deployment_id=deployment_id,
        request_id=request_id,
        trace_id=trace_id,
        q=q,
        saved=saved,
        limit=limit,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: SearchAppLogEventsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
    saved: str | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> LogEventsResponse | Problem | None:
    """Search an app's log events.

     ADR-20261018083536 internal preview, enabled by
    `FAAS_LOG_SEARCH_ENABLED=1`; otherwise 503 `log_search_unavailable`.
    Requires apps:read or admin.

    Returns one newest-first page of the app's ADR-213 log ledger. Filters
    combine with AND. A text search (`q`) covers at most 24 hours and
    answers 400 `log_search_window_too_large` beyond that; every search
    runs under a 5 second statement timeout and answers 503
    `log_search_timeout` when it trips. Pass `next_cursor` back as
    `cursor` for the following page.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (SearchAppLogEventsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):
        saved (str | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogEventsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
        source=source,
        level=level,
        route=route,
        status=status,
        deployment_id=deployment_id,
        request_id=request_id,
        trace_id=trace_id,
        q=q,
        saved=saved,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: SearchAppLogEventsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
    saved: str | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> Response[LogEventsResponse | Problem]:
    """Search an app's log events.

     ADR-20261018083536 internal preview, enabled by
    `FAAS_LOG_SEARCH_ENABLED=1`; otherwise 503 `log_search_unavailable`.
    Requires apps:read or admin.

    Returns one newest-first page of the app's ADR-213 log ledger. Filters
    combine with AND. A text search (`q`) covers at most 24 hours and
    answers 400 `log_search_window_too_large` beyond that; every search
    runs under a 5 second statement timeout and answers 503
    `log_search_timeout` when it trips. Pass `next_cursor` back as
    `cursor` for the following page.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (SearchAppLogEventsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):
        saved (str | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        source=source,
        level=level,
        route=route,
        status=status,
        deployment_id=deployment_id,
        request_id=request_id,
        trace_id=trace_id,
        q=q,
        saved=saved,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: SearchAppLogEventsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
    saved: str | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> LogEventsResponse | Problem | None:
    """Search an app's log events.

     ADR-20261018083536 internal preview, enabled by
    `FAAS_LOG_SEARCH_ENABLED=1`; otherwise 503 `log_search_unavailable`.
    Requires apps:read or admin.

    Returns one newest-first page of the app's ADR-213 log ledger. Filters
    combine with AND. A text search (`q`) covers at most 24 hours and
    answers 400 `log_search_window_too_large` beyond that; every search
    runs under a 5 second statement timeout and answers 503
    `log_search_timeout` when it trips. Pass `next_cursor` back as
    `cursor` for the following page.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (SearchAppLogEventsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):
        saved (str | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
            source=source,
            level=level,
            route=route,
            status=status,
            deployment_id=deployment_id,
            request_id=request_id,
            trace_id=trace_id,
            q=q,
            saved=saved,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
