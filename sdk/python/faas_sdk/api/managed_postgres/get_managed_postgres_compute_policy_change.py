from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_compute_policy_change import ManagedPostgresComputePolicyChange
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    change_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/compute-policy-changes/{change_id}".format(
            id=quote(str(id), safe=""),
            change_id=quote(str(change_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresComputePolicyChange | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresComputePolicyChange.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresComputePolicyChange | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresComputePolicyChange | Problem]:
    """Get managed PostgreSQL compute policy progress

     Reads a durable compute policy request for the authenticated account and database. Responses are not
    cached and omit private provider identity.

    Args:
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresComputePolicyChange | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        change_id=change_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresComputePolicyChange | Problem | None:
    """Get managed PostgreSQL compute policy progress

     Reads a durable compute policy request for the authenticated account and database. Responses are not
    cached and omit private provider identity.

    Args:
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresComputePolicyChange | Problem
    """

    return sync_detailed(
        id=id,
        change_id=change_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresComputePolicyChange | Problem]:
    """Get managed PostgreSQL compute policy progress

     Reads a durable compute policy request for the authenticated account and database. Responses are not
    cached and omit private provider identity.

    Args:
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresComputePolicyChange | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        change_id=change_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresComputePolicyChange | Problem | None:
    """Get managed PostgreSQL compute policy progress

     Reads a durable compute policy request for the authenticated account and database. Responses are not
    cached and omit private provider identity.

    Args:
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresComputePolicyChange | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            change_id=change_id,
            client=client,
        )
    ).parsed
