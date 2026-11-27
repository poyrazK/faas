from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_maintenance_status import ManagedPostgresMaintenanceStatus
from ...models.problem import Problem
from ...models.set_managed_postgres_maintenance_request import SetManagedPostgresMaintenanceRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: SetManagedPostgresMaintenanceRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/databases/{id}/maintenance-window".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresMaintenanceStatus | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresMaintenanceStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresMaintenanceStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresMaintenanceRequest,
) -> Response[ManagedPostgresMaintenanceStatus | Problem]:
    """Set a weekly UTC window for planned native PostgreSQL base refreshes.

     Available for always-on native PostgreSQL databases. Production refreshes
    verify the standby before a planned switchover. Clients can disconnect
    during a refresh. A null window restores the default refresh policy.

    Args:
        id (str):
        body (SetManagedPostgresMaintenanceRequest): Changes to the database's weekly maintenance
            schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMaintenanceStatus | Problem]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresMaintenanceRequest,
) -> ManagedPostgresMaintenanceStatus | Problem | None:
    """Set a weekly UTC window for planned native PostgreSQL base refreshes.

     Available for always-on native PostgreSQL databases. Production refreshes
    verify the standby before a planned switchover. Clients can disconnect
    during a refresh. A null window restores the default refresh policy.

    Args:
        id (str):
        body (SetManagedPostgresMaintenanceRequest): Changes to the database's weekly maintenance
            schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMaintenanceStatus | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresMaintenanceRequest,
) -> Response[ManagedPostgresMaintenanceStatus | Problem]:
    """Set a weekly UTC window for planned native PostgreSQL base refreshes.

     Available for always-on native PostgreSQL databases. Production refreshes
    verify the standby before a planned switchover. Clients can disconnect
    during a refresh. A null window restores the default refresh policy.

    Args:
        id (str):
        body (SetManagedPostgresMaintenanceRequest): Changes to the database's weekly maintenance
            schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMaintenanceStatus | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresMaintenanceRequest,
) -> ManagedPostgresMaintenanceStatus | Problem | None:
    """Set a weekly UTC window for planned native PostgreSQL base refreshes.

     Available for always-on native PostgreSQL databases. Production refreshes
    verify the standby before a planned switchover. Clients can disconnect
    during a refresh. A null window restores the default refresh policy.

    Args:
        id (str):
        body (SetManagedPostgresMaintenanceRequest): Changes to the database's weekly maintenance
            schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMaintenanceStatus | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
