from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_import_request import ManagedPostgresImportRequest
from ...models.managed_postgres_migration_progress import ManagedPostgresMigrationProgress
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    *,
    body: ManagedPostgresImportRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/migration/prepare".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresMigrationProgress | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresMigrationProgress.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresMigrationProgress | Problem]:
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
    body: ManagedPostgresImportRequest,
) -> Response[ManagedPostgresMigrationProgress | Problem]:
    """Prepare the target of a native PostgreSQL live migration for the organization.

     On the organization's database, queues schema preparation after initial table copy. Builds supported
    indexes while the subscriber continues to apply changes and reports source schema blockers. Read
    progress from the migration status endpoint.

    Args:
        organization (str):
        id (str):
        body (ManagedPostgresImportRequest): Source connection and operation identifier for
            importing a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMigrationProgress | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
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
    body: ManagedPostgresImportRequest,
) -> ManagedPostgresMigrationProgress | Problem | None:
    """Prepare the target of a native PostgreSQL live migration for the organization.

     On the organization's database, queues schema preparation after initial table copy. Builds supported
    indexes while the subscriber continues to apply changes and reports source schema blockers. Read
    progress from the migration status endpoint.

    Args:
        organization (str):
        id (str):
        body (ManagedPostgresImportRequest): Source connection and operation identifier for
            importing a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMigrationProgress | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresImportRequest,
) -> Response[ManagedPostgresMigrationProgress | Problem]:
    """Prepare the target of a native PostgreSQL live migration for the organization.

     On the organization's database, queues schema preparation after initial table copy. Builds supported
    indexes while the subscriber continues to apply changes and reports source schema blockers. Read
    progress from the migration status endpoint.

    Args:
        organization (str):
        id (str):
        body (ManagedPostgresImportRequest): Source connection and operation identifier for
            importing a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMigrationProgress | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresImportRequest,
) -> ManagedPostgresMigrationProgress | Problem | None:
    """Prepare the target of a native PostgreSQL live migration for the organization.

     On the organization's database, queues schema preparation after initial table copy. Builds supported
    indexes while the subscriber continues to apply changes and reports source schema blockers. Read
    progress from the migration status endpoint.

    Args:
        organization (str):
        id (str):
        body (ManagedPostgresImportRequest): Source connection and operation identifier for
            importing a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMigrationProgress | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
