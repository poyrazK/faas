from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_resize import ManagedPostgresResize
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    resize_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/resizes/{resize_id}".format(
            id=quote(str(id), safe=""),
            resize_id=quote(str(resize_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresResize | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresResize.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresResize | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresResize | Problem]:
    """Get managed PostgreSQL resize progress

    Args:
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        resize_id=resize_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresResize | Problem | None:
    """Get managed PostgreSQL resize progress

    Args:
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresResize | Problem
    """

    return sync_detailed(
        id=id,
        resize_id=resize_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresResize | Problem]:
    """Get managed PostgreSQL resize progress

    Args:
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        resize_id=resize_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresResize | Problem | None:
    """Get managed PostgreSQL resize progress

    Args:
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresResize | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            resize_id=resize_id,
            client=client,
        )
    ).parsed
