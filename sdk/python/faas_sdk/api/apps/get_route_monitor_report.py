from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_monitor_report import RouteMonitorReport
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-monitor/report".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteMonitorReport | None:
    if response.status_code == 200:
        response_200 = RouteMonitorReport.from_dict(response.json())

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
) -> Response[Problem | RouteMonitorReport]:
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
) -> Response[Problem | RouteMonitorReport]:
    """Read observed route budgets for the fully serving production deployment.

     Read-only evaluation of two closed UTC minute windows with a 30 second ingestion allowance. Selects
    the sole fully serving default-scope live deployment; split, incomplete, sparse or unavailable
    context is unknown. Errors require 20 represented requests and at least two errors to confirm a
    budget violation; latency requires 100 requests per window. Both windows must start after
    configuration and serving anchors. Coverage is observed_only, not an SLO or full capture. Does not
    create incidents or change traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorReport]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteMonitorReport | None:
    """Read observed route budgets for the fully serving production deployment.

     Read-only evaluation of two closed UTC minute windows with a 30 second ingestion allowance. Selects
    the sole fully serving default-scope live deployment; split, incomplete, sparse or unavailable
    context is unknown. Errors require 20 represented requests and at least two errors to confirm a
    budget violation; latency requires 100 requests per window. Both windows must start after
    configuration and serving anchors. Coverage is observed_only, not an SLO or full capture. Does not
    create incidents or change traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorReport
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteMonitorReport]:
    """Read observed route budgets for the fully serving production deployment.

     Read-only evaluation of two closed UTC minute windows with a 30 second ingestion allowance. Selects
    the sole fully serving default-scope live deployment; split, incomplete, sparse or unavailable
    context is unknown. Errors require 20 represented requests and at least two errors to confirm a
    budget violation; latency requires 100 requests per window. Both windows must start after
    configuration and serving anchors. Coverage is observed_only, not an SLO or full capture. Does not
    create incidents or change traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorReport]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteMonitorReport | None:
    """Read observed route budgets for the fully serving production deployment.

     Read-only evaluation of two closed UTC minute windows with a 30 second ingestion allowance. Selects
    the sole fully serving default-scope live deployment; split, incomplete, sparse or unavailable
    context is unknown. Errors require 20 represented requests and at least two errors to confirm a
    budget violation; latency requires 100 requests per window. Both windows must start after
    configuration and serving anchors. Coverage is observed_only, not an SLO or full capture. Does not
    create incidents or change traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorReport
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
