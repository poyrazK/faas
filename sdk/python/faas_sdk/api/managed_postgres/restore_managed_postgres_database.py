from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_database import ManagedPostgresDatabase
from ...models.problem import Problem
from ...models.restore_managed_postgres_database_request import RestoreManagedPostgresDatabaseRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    body: RestoreManagedPostgresDatabaseRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/restore".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresDatabase | Problem:
    if response.status_code == 201:
        response_201 = ManagedPostgresDatabase.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresDatabase | Problem]:
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
    body: RestoreManagedPostgresDatabaseRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresDatabase | Problem]:
    """Restore a database into a new managed PostgreSQL database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (RestoreManagedPostgresDatabaseRequest): Point-in-time restore request that creates a
            new database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresDatabase | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RestoreManagedPostgresDatabaseRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresDatabase | Problem | None:
    """Restore a database into a new managed PostgreSQL database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (RestoreManagedPostgresDatabaseRequest): Point-in-time restore request that creates a
            new database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresDatabase | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RestoreManagedPostgresDatabaseRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresDatabase | Problem]:
    """Restore a database into a new managed PostgreSQL database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (RestoreManagedPostgresDatabaseRequest): Point-in-time restore request that creates a
            new database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresDatabase | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RestoreManagedPostgresDatabaseRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresDatabase | Problem | None:
    """Restore a database into a new managed PostgreSQL database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (RestoreManagedPostgresDatabaseRequest): Point-in-time restore request that creates a
            new database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresDatabase | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
