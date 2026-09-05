import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_app_request_analytics_timeseries_method import (
    GetAppRequestAnalyticsTimeseriesMethod,
)
from ...models.problem import Problem
from ...models.request_analytics_timeseries_response import RequestAnalyticsTimeseriesResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: str | Unset = "24h",
    until: datetime.datetime | Unset = UNSET,
    route: str | Unset = UNSET,
    method: GetAppRequestAnalyticsTimeseriesMethod | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["route"] = route

    json_method: str | Unset = UNSET
    if not isinstance(method, Unset):
        json_method = method

    params["method"] = json_method

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/analytics/timeseries".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RequestAnalyticsTimeseriesResponse | None:
    if response.status_code == 200:
        response_200 = RequestAnalyticsTimeseriesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RequestAnalyticsTimeseriesResponse]:
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
    since: str | Unset = "24h",
    until: datetime.datetime | Unset = UNSET,
    route: str | Unset = UNSET,
    method: GetAppRequestAnalyticsTimeseriesMethod | Unset = UNSET,
) -> Response[Problem | RequestAnalyticsTimeseriesResponse]:
    """Request analytics time series by hour.

     Returns zero-filled UTC hourly buckets for customer request analytics.
    Each bucket contains request and error counts, error rate, cold boots,
    and weighted p50/p95/p99 latency. The window is half-open [since, until)
    and is clamped to the plan's DebugTelemetryRetentionDays.

    `since` accepts a duration such as `24h` or `7d`, or an RFC3339 start
    timestamp. `until` is an optional RFC3339 exclusive upper bound and
    defaults to now. The endpoint is read-only, IDOR-safe, and plan-gated
    by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.
        until (datetime.datetime | Unset):
        route (str | Unset):
        method (GetAppRequestAnalyticsTimeseriesMethod | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAnalyticsTimeseriesResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        route=route,
        method=method,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "24h",
    until: datetime.datetime | Unset = UNSET,
    route: str | Unset = UNSET,
    method: GetAppRequestAnalyticsTimeseriesMethod | Unset = UNSET,
) -> Problem | RequestAnalyticsTimeseriesResponse | None:
    """Request analytics time series by hour.

     Returns zero-filled UTC hourly buckets for customer request analytics.
    Each bucket contains request and error counts, error rate, cold boots,
    and weighted p50/p95/p99 latency. The window is half-open [since, until)
    and is clamped to the plan's DebugTelemetryRetentionDays.

    `since` accepts a duration such as `24h` or `7d`, or an RFC3339 start
    timestamp. `until` is an optional RFC3339 exclusive upper bound and
    defaults to now. The endpoint is read-only, IDOR-safe, and plan-gated
    by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.
        until (datetime.datetime | Unset):
        route (str | Unset):
        method (GetAppRequestAnalyticsTimeseriesMethod | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAnalyticsTimeseriesResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
        route=route,
        method=method,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "24h",
    until: datetime.datetime | Unset = UNSET,
    route: str | Unset = UNSET,
    method: GetAppRequestAnalyticsTimeseriesMethod | Unset = UNSET,
) -> Response[Problem | RequestAnalyticsTimeseriesResponse]:
    """Request analytics time series by hour.

     Returns zero-filled UTC hourly buckets for customer request analytics.
    Each bucket contains request and error counts, error rate, cold boots,
    and weighted p50/p95/p99 latency. The window is half-open [since, until)
    and is clamped to the plan's DebugTelemetryRetentionDays.

    `since` accepts a duration such as `24h` or `7d`, or an RFC3339 start
    timestamp. `until` is an optional RFC3339 exclusive upper bound and
    defaults to now. The endpoint is read-only, IDOR-safe, and plan-gated
    by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.
        until (datetime.datetime | Unset):
        route (str | Unset):
        method (GetAppRequestAnalyticsTimeseriesMethod | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAnalyticsTimeseriesResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        route=route,
        method=method,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "24h",
    until: datetime.datetime | Unset = UNSET,
    route: str | Unset = UNSET,
    method: GetAppRequestAnalyticsTimeseriesMethod | Unset = UNSET,
) -> Problem | RequestAnalyticsTimeseriesResponse | None:
    """Request analytics time series by hour.

     Returns zero-filled UTC hourly buckets for customer request analytics.
    Each bucket contains request and error counts, error rate, cold boots,
    and weighted p50/p95/p99 latency. The window is half-open [since, until)
    and is clamped to the plan's DebugTelemetryRetentionDays.

    `since` accepts a duration such as `24h` or `7d`, or an RFC3339 start
    timestamp. `until` is an optional RFC3339 exclusive upper bound and
    defaults to now. The endpoint is read-only, IDOR-safe, and plan-gated
    by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.
        until (datetime.datetime | Unset):
        route (str | Unset):
        method (GetAppRequestAnalyticsTimeseriesMethod | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAnalyticsTimeseriesResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
            route=route,
            method=method,
        )
    ).parsed
