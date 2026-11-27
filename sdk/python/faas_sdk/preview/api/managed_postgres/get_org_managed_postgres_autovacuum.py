from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_autovacuum import ManagedPostgresAutovacuum
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/autovacuum".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresAutovacuum | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresAutovacuum.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresAutovacuum | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresAutovacuum | Problem]:
    """Read a native PostgreSQL autovacuum profile and effective settings for the organization.

     On the organization's database, reads the selected profile and live settings where available. A
    parked database stays asleep.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAutovacuum | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresAutovacuum | Problem | None:
    """Read a native PostgreSQL autovacuum profile and effective settings for the organization.

     On the organization's database, reads the selected profile and live settings where available. A
    parked database stays asleep.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAutovacuum | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresAutovacuum | Problem]:
    """Read a native PostgreSQL autovacuum profile and effective settings for the organization.

     On the organization's database, reads the selected profile and live settings where available. A
    parked database stays asleep.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAutovacuum | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresAutovacuum | Problem | None:
    """Read a native PostgreSQL autovacuum profile and effective settings for the organization.

     On the organization's database, reads the selected profile and live settings where available. A
    parked database stays asleep.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAutovacuum | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
        )
    ).parsed
