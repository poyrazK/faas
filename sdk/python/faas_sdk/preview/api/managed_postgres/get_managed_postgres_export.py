from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_export import ManagedPostgresExport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    export_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/exports/{export_id}".format(
            id=quote(str(id), safe=""),
            export_id=quote(str(export_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresExport | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresExport.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresExport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    export_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresExport | Problem]:
    """Read a native PostgreSQL export and its manifest

     The ready manifest carries the snapshot time, PostgreSQL major, extension versions, size and SHA-256
    checksum.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresExport | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        export_id=export_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    export_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresExport | Problem | None:
    """Read a native PostgreSQL export and its manifest

     The ready manifest carries the snapshot time, PostgreSQL major, extension versions, size and SHA-256
    checksum.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresExport | Problem
    """

    return sync_detailed(
        id=id,
        export_id=export_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    export_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresExport | Problem]:
    """Read a native PostgreSQL export and its manifest

     The ready manifest carries the snapshot time, PostgreSQL major, extension versions, size and SHA-256
    checksum.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresExport | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        export_id=export_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    export_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresExport | Problem | None:
    """Read a native PostgreSQL export and its manifest

     The ready manifest carries the snapshot time, PostgreSQL major, extension versions, size and SHA-256
    checksum.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresExport | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            export_id=export_id,
            client=client,
        )
    ).parsed
