from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_import_source import ManagedPostgresImportSource
from ...models.managed_postgres_import_status import ManagedPostgresImportStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ManagedPostgresImportSource,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/imports/check".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresImportStatus | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresImportStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresImportStatus | Problem]:
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
    body: ManagedPostgresImportSource,
) -> Response[ManagedPostgresImportStatus | Problem]:
    """Check a PostgreSQL source and an empty native import target

     Checks the source connection, major version, extensions, target emptiness and estimated storage. May
    wake the target to inspect it. Requires PostgreSQL management access.

    Args:
        id (str):
        body (ManagedPostgresImportSource): Connection details for a PostgreSQL source to inspect
            or import.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresImportStatus | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresImportSource,
) -> ManagedPostgresImportStatus | Problem | None:
    """Check a PostgreSQL source and an empty native import target

     Checks the source connection, major version, extensions, target emptiness and estimated storage. May
    wake the target to inspect it. Requires PostgreSQL management access.

    Args:
        id (str):
        body (ManagedPostgresImportSource): Connection details for a PostgreSQL source to inspect
            or import.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresImportStatus | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresImportSource,
) -> Response[ManagedPostgresImportStatus | Problem]:
    """Check a PostgreSQL source and an empty native import target

     Checks the source connection, major version, extensions, target emptiness and estimated storage. May
    wake the target to inspect it. Requires PostgreSQL management access.

    Args:
        id (str):
        body (ManagedPostgresImportSource): Connection details for a PostgreSQL source to inspect
            or import.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresImportStatus | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresImportSource,
) -> ManagedPostgresImportStatus | Problem | None:
    """Check a PostgreSQL source and an empty native import target

     Checks the source connection, major version, extensions, target emptiness and estimated storage. May
    wake the target to inspect it. Requires PostgreSQL management access.

    Args:
        id (str):
        body (ManagedPostgresImportSource): Connection details for a PostgreSQL source to inspect
            or import.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresImportStatus | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
