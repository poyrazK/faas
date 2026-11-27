from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_resources_response import RouteResourcesResponse
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
        "url": "/v1/apps/{slug}/analytics/route-resources".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteResourcesResponse | None:
    if response.status_code == 200:
        response_200 = RouteResourcesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteResourcesResponse]:
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
) -> Response[Problem | RouteResourcesResponse]:
    """Which routes the app's memory and CPU peaks come from.

     Joins each instance's per-minute memory and CPU with the routes that
    instance served in the same minutes. A memory peak is a minute whose
    memory rose by at least high_memory_rise_mb above where it started; a
    CPU peak is a minute whose busiest second reached 80% of the CPU limit.
    For each route the response gives the share of peaks it was in flight
    for, how often it ran at all, and the p95 peak in minutes with and
    without it. This is a correlation over minutes, not a per-request
    measurement: a route that runs in every minute shares every peak.
    Available only where route resource peaks are enabled for the account.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteResourcesResponse]
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
) -> Problem | RouteResourcesResponse | None:
    """Which routes the app's memory and CPU peaks come from.

     Joins each instance's per-minute memory and CPU with the routes that
    instance served in the same minutes. A memory peak is a minute whose
    memory rose by at least high_memory_rise_mb above where it started; a
    CPU peak is a minute whose busiest second reached 80% of the CPU limit.
    For each route the response gives the share of peaks it was in flight
    for, how often it ran at all, and the p95 peak in minutes with and
    without it. This is a correlation over minutes, not a per-request
    measurement: a route that runs in every minute shares every peak.
    Available only where route resource peaks are enabled for the account.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteResourcesResponse
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
) -> Response[Problem | RouteResourcesResponse]:
    """Which routes the app's memory and CPU peaks come from.

     Joins each instance's per-minute memory and CPU with the routes that
    instance served in the same minutes. A memory peak is a minute whose
    memory rose by at least high_memory_rise_mb above where it started; a
    CPU peak is a minute whose busiest second reached 80% of the CPU limit.
    For each route the response gives the share of peaks it was in flight
    for, how often it ran at all, and the p95 peak in minutes with and
    without it. This is a correlation over minutes, not a per-request
    measurement: a route that runs in every minute shares every peak.
    Available only where route resource peaks are enabled for the account.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteResourcesResponse]
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
) -> Problem | RouteResourcesResponse | None:
    """Which routes the app's memory and CPU peaks come from.

     Joins each instance's per-minute memory and CPU with the routes that
    instance served in the same minutes. A memory peak is a minute whose
    memory rose by at least high_memory_rise_mb above where it started; a
    CPU peak is a minute whose busiest second reached 80% of the CPU limit.
    For each route the response gives the share of peaks it was in flight
    for, how often it ran at all, and the p95 peak in minutes with and
    without it. This is a correlation over minutes, not a per-request
    measurement: a route that runs in every minute shares every peak.
    Available only where route resource peaks are enabled for the account.

    Args:
        slug (str):
        since (str | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteResourcesResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
        )
    ).parsed
