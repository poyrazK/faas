from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_metrics_response import AppMetricsResponse
from ...models.get_app_metrics_range import GetAppMetricsRange
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    range_: GetAppMetricsRange | Unset = "5m",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_range_: str | Unset = UNSET
    if not isinstance(range_, Unset):
        json_range_ = range_

    params["range"] = json_range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/metrics".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppMetricsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppMetricsResponse.from_dict(response.json())

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
) -> Response[AppMetricsResponse | Problem]:
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
    range_: GetAppMetricsRange | Unset = "5m",
) -> Response[AppMetricsResponse | Problem]:
    r"""Per-app request metrics (issue

     Time-windowed rollup of one app's gateway activity. The `range`
    parameter is a closed vocabulary bounded by Prometheus
    retention (`prom_retention_days: 15`):

      `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`

    Wake latency (`wake_p95_ms`) is the FLEET p95
    (`gateway_wake_latency_seconds` is unlabeled). On Prometheus
    failure the endpoint returns 200 with zeroed fields and
    `source: \"degraded: <reason>\"`, matching the public status
    page contract.

    Args:
        slug (str):
        range_ (GetAppMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppMetricsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        range_=range_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppMetricsRange | Unset = "5m",
) -> AppMetricsResponse | Problem | None:
    r"""Per-app request metrics (issue

     Time-windowed rollup of one app's gateway activity. The `range`
    parameter is a closed vocabulary bounded by Prometheus
    retention (`prom_retention_days: 15`):

      `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`

    Wake latency (`wake_p95_ms`) is the FLEET p95
    (`gateway_wake_latency_seconds` is unlabeled). On Prometheus
    failure the endpoint returns 200 with zeroed fields and
    `source: \"degraded: <reason>\"`, matching the public status
    page contract.

    Args:
        slug (str):
        range_ (GetAppMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppMetricsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        range_=range_,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppMetricsRange | Unset = "5m",
) -> Response[AppMetricsResponse | Problem]:
    r"""Per-app request metrics (issue

     Time-windowed rollup of one app's gateway activity. The `range`
    parameter is a closed vocabulary bounded by Prometheus
    retention (`prom_retention_days: 15`):

      `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`

    Wake latency (`wake_p95_ms`) is the FLEET p95
    (`gateway_wake_latency_seconds` is unlabeled). On Prometheus
    failure the endpoint returns 200 with zeroed fields and
    `source: \"degraded: <reason>\"`, matching the public status
    page contract.

    Args:
        slug (str):
        range_ (GetAppMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppMetricsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        range_=range_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetAppMetricsRange | Unset = "5m",
) -> AppMetricsResponse | Problem | None:
    r"""Per-app request metrics (issue

     Time-windowed rollup of one app's gateway activity. The `range`
    parameter is a closed vocabulary bounded by Prometheus
    retention (`prom_retention_days: 15`):

      `5m` (default) | `15m` | `1h` | `6h` | `24h` | `7d` | `15d`

    Wake latency (`wake_p95_ms`) is the FLEET p95
    (`gateway_wake_latency_seconds` is unlabeled). On Prometheus
    failure the endpoint returns 200 with zeroed fields and
    `source: \"degraded: <reason>\"`, matching the public status
    page contract.

    Args:
        slug (str):
        range_ (GetAppMetricsRange | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppMetricsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            range_=range_,
        )
    ).parsed
