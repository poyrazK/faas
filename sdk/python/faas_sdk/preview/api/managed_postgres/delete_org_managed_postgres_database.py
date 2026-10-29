from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_database import ManagedPostgresDatabase
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    id: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresDatabase | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresDatabase.from_dict(response.json())

        return response_200

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresDatabase | Problem]:
    """Delete a company PostgreSQL resource.

     An owner or admin requests provider cleanup. Active bindings and retained restore dependencies must
    be removed first; accounting evidence remains available.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresDatabase | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        idempotency_key=idempotency_key,
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
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresDatabase | Problem | None:
    """Delete a company PostgreSQL resource.

     An owner or admin requests provider cleanup. Active bindings and retained restore dependencies must
    be removed first; accounting evidence remains available.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresDatabase | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresDatabase | Problem]:
    """Delete a company PostgreSQL resource.

     An owner or admin requests provider cleanup. Active bindings and retained restore dependencies must
    be removed first; accounting evidence remains available.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresDatabase | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresDatabase | Problem | None:
    """Delete a company PostgreSQL resource.

     An owner or admin requests provider cleanup. Active bindings and retained restore dependencies must
    be removed first; accounting evidence remains available.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresDatabase | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
