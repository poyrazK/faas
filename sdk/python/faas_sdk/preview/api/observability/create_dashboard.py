from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.dashboard import Dashboard
from ...models.dashboard_request import DashboardRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: DashboardRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dashboards",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Dashboard | Problem:
    if response.status_code == 201:
        response_201 = Dashboard.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Dashboard | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Response[Dashboard | Problem]:
    """Create a custom dashboard.

     Saves a named dashboard of up to 24 widgets. Each widget references a
    signal by kind (app_metric, log_count, custom_metric, slo) and an app
    the account owns. An account keeps at most 20 dashboards.

    Args:
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Dashboard | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Dashboard | Problem | None:
    """Create a custom dashboard.

     Saves a named dashboard of up to 24 widgets. Each widget references a
    signal by kind (app_metric, log_count, custom_metric, slo) and an app
    the account owns. An account keeps at most 20 dashboards.

    Args:
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Dashboard | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Response[Dashboard | Problem]:
    """Create a custom dashboard.

     Saves a named dashboard of up to 24 widgets. Each widget references a
    signal by kind (app_metric, log_count, custom_metric, slo) and an app
    the account owns. An account keeps at most 20 dashboards.

    Args:
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Dashboard | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Dashboard | Problem | None:
    """Create a custom dashboard.

     Saves a named dashboard of up to 24 widgets. Each widget references a
    signal by kind (app_metric, log_count, custom_metric, slo) and an app
    the account owns. An account keeps at most 20 dashboards.

    Args:
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Dashboard | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
