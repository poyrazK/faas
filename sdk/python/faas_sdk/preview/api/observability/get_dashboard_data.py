from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.dashboard_data import DashboardData
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/dashboards/{id}/data".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DashboardData | Problem:
    if response.status_code == 200:
        response_200 = DashboardData.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DashboardData | Problem]:
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
) -> Response[DashboardData | Problem]:
    """Read a custom dashboard's widgets.

     Resolves every widget for its window through the code behind the
    per-app endpoints: request metrics, log search counts, pushed custom
    metrics and SLO status. A widget that cannot be read carries `error`
    and no value; the rest still resolve.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DashboardData | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> DashboardData | Problem | None:
    """Read a custom dashboard's widgets.

     Resolves every widget for its window through the code behind the
    per-app endpoints: request metrics, log search counts, pushed custom
    metrics and SLO status. A widget that cannot be read carries `error`
    and no value; the rest still resolve.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DashboardData | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DashboardData | Problem]:
    """Read a custom dashboard's widgets.

     Resolves every widget for its window through the code behind the
    per-app endpoints: request metrics, log search counts, pushed custom
    metrics and SLO status. A widget that cannot be read carries `error`
    and no value; the rest still resolve.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DashboardData | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> DashboardData | Problem | None:
    """Read a custom dashboard's widgets.

     Resolves every widget for its window through the code behind the
    per-app endpoints: request metrics, log search counts, pushed custom
    metrics and SLO status. A widget that cannot be read carries `error`
    and no value; the rest still resolve.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DashboardData | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
