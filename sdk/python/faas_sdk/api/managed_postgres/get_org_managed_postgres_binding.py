from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_binding import ManagedPostgresBinding
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/bindings/{id}".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresBinding | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresBinding.from_dict(response.json())

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
) -> Response[ManagedPostgresBinding | Problem]:
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
) -> Response[ManagedPostgresBinding | Problem]:
    """Inspect a company database binding.

     Resolves ownership through the binding's database before exposing metadata. Current organization
    membership is required even when the binding retains a creator namespace.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBinding | Problem]
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
) -> ManagedPostgresBinding | Problem | None:
    """Inspect a company database binding.

     Resolves ownership through the binding's database before exposing metadata. Current organization
    membership is required even when the binding retains a creator namespace.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBinding | Problem
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
) -> Response[ManagedPostgresBinding | Problem]:
    """Inspect a company database binding.

     Resolves ownership through the binding's database before exposing metadata. Current organization
    membership is required even when the binding retains a creator namespace.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBinding | Problem]
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
) -> ManagedPostgresBinding | Problem | None:
    """Inspect a company database binding.

     Resolves ownership through the binding's database before exposing metadata. Current organization
    membership is required even when the binding retains a creator namespace.

    Args:
        organization (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBinding | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
        )
    ).parsed
