from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apps_metrics_response import AppsMetricsResponse
from ...models.get_apps_metrics_range import GetAppsMetricsRange
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    range_: GetAppsMetricsRange | Unset = "5m",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_range_: str | Unset = UNSET
    if not isinstance(range_, Unset):
        json_range_ = range_

    params["range"] = json_range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/metrics",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppsMetricsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppsMetricsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppsMetricsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppsMetricsRange | Unset = "5m",
) -> Response[AppsMetricsResponse | Problem]:
    r"""Account-wide per-app metrics rollup.

     One call replaces N per-app `/v1/apps/{slug}/metrics` calls
    (issue #393). Returns the same `AppMetricsResponse` shape
    per app, keyed by `app_slug`, so the dashboard can render
    all apps on a single page without a per-app fan-out.

    Range is the closed vocabulary from the per-app endpoint:
    `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`.
    Prometheus failure short-circuits the entire response
    (never partial-populated) and emits `source:
    \"degraded: <reason>\"` with zeroed `apps`, matching the
    per-app contract exactly.

    PromQL cost: 6 round-trips regardless of N apps (vs. 7N
    for the naive per-app loop) — see `pkg/promql.Client.QueryMap`
    and `Client.QueryBuckets`. This rollup exposes the same
    Hobby+ signals as the per-app endpoint, so Free accounts
    receive 402.

    Args:
        range_ (GetAppsMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppsMetricsResponse | Problem]
    """

    kwargs = _get_kwargs(
        range_=range_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppsMetricsRange | Unset = "5m",
) -> AppsMetricsResponse | Problem | None:
    r"""Account-wide per-app metrics rollup.

     One call replaces N per-app `/v1/apps/{slug}/metrics` calls
    (issue #393). Returns the same `AppMetricsResponse` shape
    per app, keyed by `app_slug`, so the dashboard can render
    all apps on a single page without a per-app fan-out.

    Range is the closed vocabulary from the per-app endpoint:
    `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`.
    Prometheus failure short-circuits the entire response
    (never partial-populated) and emits `source:
    \"degraded: <reason>\"` with zeroed `apps`, matching the
    per-app contract exactly.

    PromQL cost: 6 round-trips regardless of N apps (vs. 7N
    for the naive per-app loop) — see `pkg/promql.Client.QueryMap`
    and `Client.QueryBuckets`. This rollup exposes the same
    Hobby+ signals as the per-app endpoint, so Free accounts
    receive 402.

    Args:
        range_ (GetAppsMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppsMetricsResponse | Problem
    """

    return sync_detailed(
        client=client,
        range_=range_,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppsMetricsRange | Unset = "5m",
) -> Response[AppsMetricsResponse | Problem]:
    r"""Account-wide per-app metrics rollup.

     One call replaces N per-app `/v1/apps/{slug}/metrics` calls
    (issue #393). Returns the same `AppMetricsResponse` shape
    per app, keyed by `app_slug`, so the dashboard can render
    all apps on a single page without a per-app fan-out.

    Range is the closed vocabulary from the per-app endpoint:
    `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`.
    Prometheus failure short-circuits the entire response
    (never partial-populated) and emits `source:
    \"degraded: <reason>\"` with zeroed `apps`, matching the
    per-app contract exactly.

    PromQL cost: 6 round-trips regardless of N apps (vs. 7N
    for the naive per-app loop) — see `pkg/promql.Client.QueryMap`
    and `Client.QueryBuckets`. This rollup exposes the same
    Hobby+ signals as the per-app endpoint, so Free accounts
    receive 402.

    Args:
        range_ (GetAppsMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppsMetricsResponse | Problem]
    """

    kwargs = _get_kwargs(
        range_=range_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppsMetricsRange | Unset = "5m",
) -> AppsMetricsResponse | Problem | None:
    r"""Account-wide per-app metrics rollup.

     One call replaces N per-app `/v1/apps/{slug}/metrics` calls
    (issue #393). Returns the same `AppMetricsResponse` shape
    per app, keyed by `app_slug`, so the dashboard can render
    all apps on a single page without a per-app fan-out.

    Range is the closed vocabulary from the per-app endpoint:
    `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`.
    Prometheus failure short-circuits the entire response
    (never partial-populated) and emits `source:
    \"degraded: <reason>\"` with zeroed `apps`, matching the
    per-app contract exactly.

    PromQL cost: 6 round-trips regardless of N apps (vs. 7N
    for the naive per-app loop) — see `pkg/promql.Client.QueryMap`
    and `Client.QueryBuckets`. This rollup exposes the same
    Hobby+ signals as the per-app endpoint, so Free accounts
    receive 402.

    Args:
        range_ (GetAppsMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppsMetricsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            range_=range_,
        )
    ).parsed
