from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.dashboard import Dashboard
from ...models.dashboard_request import DashboardRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: DashboardRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/dashboards/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Dashboard | Problem:
    if response.status_code == 200:
        response_200 = Dashboard.from_dict(response.json())

        return response_200

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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Response[Dashboard | Problem]:
    """Replace a custom dashboard.

     Replaces the dashboard's name and widgets.

    Args:
        id (UUID):
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Dashboard | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Dashboard | Problem | None:
    """Replace a custom dashboard.

     Replaces the dashboard's name and widgets.

    Args:
        id (UUID):
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Dashboard | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Response[Dashboard | Problem]:
    """Replace a custom dashboard.

     Replaces the dashboard's name and widgets.

    Args:
        id (UUID):
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Dashboard | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardRequest,
) -> Dashboard | Problem | None:
    """Replace a custom dashboard.

     Replaces the dashboard's name and widgets.

    Args:
        id (UUID):
        body (DashboardRequest): Body of a dashboard create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Dashboard | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
