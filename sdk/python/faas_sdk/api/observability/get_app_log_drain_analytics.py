from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_log_drain_analytics_response import AppLogDrainAnalyticsResponse
from ...models.get_app_log_drain_analytics_window import (
    GetAppLogDrainAnalyticsWindow,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    window: GetAppLogDrainAnalyticsWindow | Unset = "24h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/log-drains/{id}/analytics".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppLogDrainAnalyticsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppLogDrainAnalyticsResponse.from_dict(response.json())

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
) -> Response[AppLogDrainAnalyticsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppLogDrainAnalyticsWindow | Unset = "24h",
) -> Response[AppLogDrainAnalyticsResponse | Problem]:
    """Fetch hourly delivery analytics for a runtime log destination.

     Returns bounded, customer-safe hourly delivery history. The default
    window is 24 hours; supported windows are 1h, 24h, 7d, and 30d.
    Success rate is delivered records divided by delivered, failed, and
    dropped terminal outcomes. Average latency includes queue wait and
    endpoint time. The platform retains at least 30 days of samples.

    Args:
        slug (str):
        id (str):
        window (GetAppLogDrainAnalyticsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppLogDrainAnalyticsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        window=window,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppLogDrainAnalyticsWindow | Unset = "24h",
) -> AppLogDrainAnalyticsResponse | Problem | None:
    """Fetch hourly delivery analytics for a runtime log destination.

     Returns bounded, customer-safe hourly delivery history. The default
    window is 24 hours; supported windows are 1h, 24h, 7d, and 30d.
    Success rate is delivered records divided by delivered, failed, and
    dropped terminal outcomes. Average latency includes queue wait and
    endpoint time. The platform retains at least 30 days of samples.

    Args:
        slug (str):
        id (str):
        window (GetAppLogDrainAnalyticsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppLogDrainAnalyticsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        window=window,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppLogDrainAnalyticsWindow | Unset = "24h",
) -> Response[AppLogDrainAnalyticsResponse | Problem]:
    """Fetch hourly delivery analytics for a runtime log destination.

     Returns bounded, customer-safe hourly delivery history. The default
    window is 24 hours; supported windows are 1h, 24h, 7d, and 30d.
    Success rate is delivered records divided by delivered, failed, and
    dropped terminal outcomes. Average latency includes queue wait and
    endpoint time. The platform retains at least 30 days of samples.

    Args:
        slug (str):
        id (str):
        window (GetAppLogDrainAnalyticsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppLogDrainAnalyticsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        window=window,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppLogDrainAnalyticsWindow | Unset = "24h",
) -> AppLogDrainAnalyticsResponse | Problem | None:
    """Fetch hourly delivery analytics for a runtime log destination.

     Returns bounded, customer-safe hourly delivery history. The default
    window is 24 hours; supported windows are 1h, 24h, 7d, and 30d.
    Success rate is delivered records divided by delivered, failed, and
    dropped terminal outcomes. Average latency includes queue wait and
    endpoint time. The platform retains at least 30 days of samples.

    Args:
        slug (str):
        id (str):
        window (GetAppLogDrainAnalyticsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppLogDrainAnalyticsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            window=window,
        )
    ).parsed
