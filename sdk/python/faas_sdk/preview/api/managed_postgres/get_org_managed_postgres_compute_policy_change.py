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
    organization: str,
    id: str,
    change_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/compute-policy-changes/{change_id}".format(
            organization=quote(str(organization), safe=""),
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
) -> Response[ManagedPostgresComputePolicyChange | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresComputePolicyChange | Problem]:
    """Read a company database compute-policy operation.

     Returns the durable policy change recorded for the addressed company database. The organization
    permission check also applies to old operation UUIDs.

    Args:
        organization (str):
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresComputePolicyChange | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        change_id=change_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresComputePolicyChange | Problem | None:
    """Read a company database compute-policy operation.

     Returns the durable policy change recorded for the addressed company database. The organization
    permission check also applies to old operation UUIDs.

    Args:
        organization (str):
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresComputePolicyChange | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        change_id=change_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresComputePolicyChange | Problem]:
    """Read a company database compute-policy operation.

     Returns the durable policy change recorded for the addressed company database. The organization
    permission check also applies to old operation UUIDs.

    Args:
        organization (str):
        id (str):
        change_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresComputePolicyChange | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        change_id=change_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    change_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresComputePolicyChange | Problem | None:
    """Read a company database compute-policy operation.

     Returns the durable policy change recorded for the addressed company database. The organization
    permission check also applies to old operation UUIDs.

    Args:
        organization (str):
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
            organization=organization,
            id=id,
            change_id=change_id,
            client=client,
        )
    ).parsed
