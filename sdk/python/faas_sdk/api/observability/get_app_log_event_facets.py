import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.get_app_log_event_facets_source import GetAppLogEventFacetsSource
from ...models.log_event_facets_response import LogEventFacetsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: GetAppLogEventFacetsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
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

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/log-events/facets".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> LogEventFacetsResponse | Problem:
    if response.status_code == 200:
        response_200 = LogEventFacetsResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[LogEventFacetsResponse | Problem]:
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
    source: GetAppLogEventFacetsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
) -> Response[LogEventFacetsResponse | Problem]:
    """Count an app's log events by facet.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Takes the same filters as the search and counts the matches by
    source, level, status class, and the ten busiest routes. Counts sum
    each event's occurrences. The window is at most 7 days.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (GetAppLogEventFacetsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogEventFacetsResponse | Problem]
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
    source: GetAppLogEventFacetsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
) -> LogEventFacetsResponse | Problem | None:
    """Count an app's log events by facet.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Takes the same filters as the search and counts the matches by
    source, level, status class, and the ten busiest routes. Counts sum
    each event's occurrences. The window is at most 7 days.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (GetAppLogEventFacetsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogEventFacetsResponse | Problem
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
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: GetAppLogEventFacetsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
) -> Response[LogEventFacetsResponse | Problem]:
    """Count an app's log events by facet.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Takes the same filters as the search and counts the matches by
    source, level, status class, and the ten busiest routes. Counts sum
    each event's occurrences. The window is at most 7 days.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (GetAppLogEventFacetsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogEventFacetsResponse | Problem]
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
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    source: GetAppLogEventFacetsSource | Unset = UNSET,
    level: str | Unset = UNSET,
    route: str | Unset = UNSET,
    status: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    request_id: str | Unset = UNSET,
    trace_id: str | Unset = UNSET,
    q: str | Unset = UNSET,
) -> LogEventFacetsResponse | Problem | None:
    """Count an app's log events by facet.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Takes the same filters as the search and counts the matches by
    source, level, status class, and the ten busiest routes. Counts sum
    each event's occurrences. The window is at most 7 days.

    Args:
        slug (str):
        since (str | Unset):
        until (datetime.datetime | Unset):
        source (GetAppLogEventFacetsSource | Unset):
        level (str | Unset):  Example: error,warn.
        route (str | Unset):
        status (str | Unset):  Example: 5xx.
        deployment_id (UUID | Unset):
        request_id (str | Unset):
        trace_id (str | Unset):
        q (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogEventFacetsResponse | Problem
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
        )
    ).parsed
