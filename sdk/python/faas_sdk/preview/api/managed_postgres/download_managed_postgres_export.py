from http import HTTPStatus
from io import BytesIO
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import File, Response


def _get_kwargs(
    id: str,
    export_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/exports/{export_id}/download".format(
            id=quote(str(id), safe=""),
            export_id=quote(str(export_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> File | Problem:
    if response.status_code == 200:
        response_200 = File(payload=BytesIO(response.content))

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[File | Problem]:
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
) -> Response[File | Problem]:
    """Download a completed native PostgreSQL export

     Streams the private PostgreSQL custom archive before its expiry. Authenticate with PostgreSQL
    management access.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
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
) -> File | Problem | None:
    """Download a completed native PostgreSQL export

     Streams the private PostgreSQL custom archive before its expiry. Authenticate with PostgreSQL
    management access.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
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
) -> Response[File | Problem]:
    """Download a completed native PostgreSQL export

     Streams the private PostgreSQL custom archive before its expiry. Authenticate with PostgreSQL
    management access.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
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
) -> File | Problem | None:
    """Download a completed native PostgreSQL export

     Streams the private PostgreSQL custom archive before its expiry. Authenticate with PostgreSQL
    management access.

    Args:
        id (str):
        export_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            export_id=export_id,
            client=client,
        )
    ).parsed
