from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_upgrade_rehearsal import ManagedPostgresUpgradeRehearsal
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    rehearsal_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/upgrade-rehearsals/{rehearsal_id}".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
            rehearsal_id=quote(str(rehearsal_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresUpgradeRehearsal | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresUpgradeRehearsal.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresUpgradeRehearsal | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    id: str,
    rehearsal_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresUpgradeRehearsal | Problem]:
    """Read a native PostgreSQL upgrade rehearsal

     Reads the rehearsal progress or outcome for the selected source database.

    Args:
        organization (str):
        id (str):
        rehearsal_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUpgradeRehearsal | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        rehearsal_id=rehearsal_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    rehearsal_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresUpgradeRehearsal | Problem | None:
    """Read a native PostgreSQL upgrade rehearsal

     Reads the rehearsal progress or outcome for the selected source database.

    Args:
        organization (str):
        id (str):
        rehearsal_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUpgradeRehearsal | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        rehearsal_id=rehearsal_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    rehearsal_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresUpgradeRehearsal | Problem]:
    """Read a native PostgreSQL upgrade rehearsal

     Reads the rehearsal progress or outcome for the selected source database.

    Args:
        organization (str):
        id (str):
        rehearsal_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUpgradeRehearsal | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        rehearsal_id=rehearsal_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    rehearsal_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresUpgradeRehearsal | Problem | None:
    """Read a native PostgreSQL upgrade rehearsal

     Reads the rehearsal progress or outcome for the selected source database.

    Args:
        organization (str):
        id (str):
        rehearsal_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUpgradeRehearsal | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            rehearsal_id=rehearsal_id,
            client=client,
        )
    ).parsed
