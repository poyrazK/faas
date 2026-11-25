from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.enable_org_managed_postgres_extension_extension import (
    EnableOrgManagedPostgresExtensionExtension,
)
from ...models.managed_postgres_extensions import ManagedPostgresExtensions
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    extension: EnableOrgManagedPostgresExtensionExtension,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/extensions/{extension}".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
            extension=quote(str(extension), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresExtensions | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresExtensions.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresExtensions | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    id: str,
    extension: EnableOrgManagedPostgresExtensionExtension,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresExtensions | Problem]:
    """Enable a managed PostgreSQL extension

     Installs the default version of the extension on the native primary. Repeating the operation
    preserves an installed version.

    Args:
        organization (str):
        id (str):
        extension (EnableOrgManagedPostgresExtensionExtension):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresExtensions | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        extension=extension,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    extension: EnableOrgManagedPostgresExtensionExtension,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresExtensions | Problem | None:
    """Enable a managed PostgreSQL extension

     Installs the default version of the extension on the native primary. Repeating the operation
    preserves an installed version.

    Args:
        organization (str):
        id (str):
        extension (EnableOrgManagedPostgresExtensionExtension):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresExtensions | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        extension=extension,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    extension: EnableOrgManagedPostgresExtensionExtension,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresExtensions | Problem]:
    """Enable a managed PostgreSQL extension

     Installs the default version of the extension on the native primary. Repeating the operation
    preserves an installed version.

    Args:
        organization (str):
        id (str):
        extension (EnableOrgManagedPostgresExtensionExtension):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresExtensions | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        extension=extension,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    extension: EnableOrgManagedPostgresExtensionExtension,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresExtensions | Problem | None:
    """Enable a managed PostgreSQL extension

     Installs the default version of the extension on the native primary. Repeating the operation
    preserves an installed version.

    Args:
        organization (str):
        id (str):
        extension (EnableOrgManagedPostgresExtensionExtension):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresExtensions | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            extension=extension,
            client=client,
        )
    ).parsed
