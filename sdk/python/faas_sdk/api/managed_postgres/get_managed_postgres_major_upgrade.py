from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_major_upgrade import ManagedPostgresMajorUpgrade
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    upgrade_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/upgrades/{upgrade_id}".format(
            id=quote(str(id), safe=""),
            upgrade_id=quote(str(upgrade_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresMajorUpgrade | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresMajorUpgrade.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresMajorUpgrade | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    upgrade_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresMajorUpgrade | Problem]:
    """Get managed PostgreSQL major upgrade progress

     Reads a durable major upgrade for the authenticated account and database. Responses are not cached
    and omit private provider identity.

    Args:
        id (str):
        upgrade_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMajorUpgrade | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        upgrade_id=upgrade_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    upgrade_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresMajorUpgrade | Problem | None:
    """Get managed PostgreSQL major upgrade progress

     Reads a durable major upgrade for the authenticated account and database. Responses are not cached
    and omit private provider identity.

    Args:
        id (str):
        upgrade_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMajorUpgrade | Problem
    """

    return sync_detailed(
        id=id,
        upgrade_id=upgrade_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    upgrade_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresMajorUpgrade | Problem]:
    """Get managed PostgreSQL major upgrade progress

     Reads a durable major upgrade for the authenticated account and database. Responses are not cached
    and omit private provider identity.

    Args:
        id (str):
        upgrade_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMajorUpgrade | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        upgrade_id=upgrade_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    upgrade_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresMajorUpgrade | Problem | None:
    """Get managed PostgreSQL major upgrade progress

     Reads a durable major upgrade for the authenticated account and database. Responses are not cached
    and omit private provider identity.

    Args:
        id (str):
        upgrade_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMajorUpgrade | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            upgrade_id=upgrade_id,
            client=client,
        )
    ).parsed
