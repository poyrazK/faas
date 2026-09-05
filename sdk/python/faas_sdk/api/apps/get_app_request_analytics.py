from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.request_analytics_response import RequestAnalyticsResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: str | Unset = "24h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/analytics".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RequestAnalyticsResponse | None:
    if response.status_code == 200:
        response_200 = RequestAnalyticsResponse.from_dict(response.json())

        return response_200

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
) -> Response[Problem | RequestAnalyticsResponse]:
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
) -> Response[Problem | RequestAnalyticsResponse]:
    """Aggregated historical request analytics.

     Returns an aggregate request overview for one app: total requests,
    errors, cold boots, weighted p50/p95/p99 latency, and the top
    route/method combinations. This is the customer analytics surface;
    request identifiers and trace payloads remain on the debugger routes.

    `since` accepts a duration such as `24h` or `7d` and defaults to
    `24h`. The effective window is clamped to the plan's
    `DebugTelemetryRetentionDays` (Hobby 3d, Pro 7d, Scale 14d).
    `window_clamped` tells callers when the requested lookback was wider
    than the retained telemetry. The response contains at most 50 route
    rows; `routes_truncated` indicates that more routes matched.

    Counts and percentiles include the recorder's collapsed row `count`,
    so the result represents original requests rather than stored rows.
    The endpoint is read-only, IDOR-safe, and plan-gated by
    `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAnalyticsResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
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
) -> Problem | RequestAnalyticsResponse | None:
    """Aggregated historical request analytics.

     Returns an aggregate request overview for one app: total requests,
    errors, cold boots, weighted p50/p95/p99 latency, and the top
    route/method combinations. This is the customer analytics surface;
    request identifiers and trace payloads remain on the debugger routes.

    `since` accepts a duration such as `24h` or `7d` and defaults to
    `24h`. The effective window is clamped to the plan's
    `DebugTelemetryRetentionDays` (Hobby 3d, Pro 7d, Scale 14d).
    `window_clamped` tells callers when the requested lookback was wider
    than the retained telemetry. The response contains at most 50 route
    rows; `routes_truncated` indicates that more routes matched.

    Counts and percentiles include the recorder's collapsed row `count`,
    so the result represents original requests rather than stored rows.
    The endpoint is read-only, IDOR-safe, and plan-gated by
    `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAnalyticsResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "24h",
) -> Response[Problem | RequestAnalyticsResponse]:
    """Aggregated historical request analytics.

     Returns an aggregate request overview for one app: total requests,
    errors, cold boots, weighted p50/p95/p99 latency, and the top
    route/method combinations. This is the customer analytics surface;
    request identifiers and trace payloads remain on the debugger routes.

    `since` accepts a duration such as `24h` or `7d` and defaults to
    `24h`. The effective window is clamped to the plan's
    `DebugTelemetryRetentionDays` (Hobby 3d, Pro 7d, Scale 14d).
    `window_clamped` tells callers when the requested lookback was wider
    than the retained telemetry. The response contains at most 50 route
    rows; `routes_truncated` indicates that more routes matched.

    Counts and percentiles include the recorder's collapsed row `count`,
    so the result represents original requests rather than stored rows.
    The endpoint is read-only, IDOR-safe, and plan-gated by
    `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAnalyticsResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "24h",
) -> Problem | RequestAnalyticsResponse | None:
    """Aggregated historical request analytics.

     Returns an aggregate request overview for one app: total requests,
    errors, cold boots, weighted p50/p95/p99 latency, and the top
    route/method combinations. This is the customer analytics surface;
    request identifiers and trace payloads remain on the debugger routes.

    `since` accepts a duration such as `24h` or `7d` and defaults to
    `24h`. The effective window is clamped to the plan's
    `DebugTelemetryRetentionDays` (Hobby 3d, Pro 7d, Scale 14d).
    `window_clamped` tells callers when the requested lookback was wider
    than the retained telemetry. The response contains at most 50 route
    rows; `routes_truncated` indicates that more routes matched.

    Counts and percentiles include the recorder's collapsed row `count`,
    so the result represents original requests rather than stored rows.
    The endpoint is read-only, IDOR-safe, and plan-gated by
    `DebugTelemetryEnabled`.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAnalyticsResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
        )
    ).parsed
