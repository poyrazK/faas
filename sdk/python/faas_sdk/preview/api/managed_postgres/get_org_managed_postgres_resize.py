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
    organization: str,
    id: str,
    resize_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/resizes/{resize_id}".format(
            organization=quote(str(organization), safe=""),
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
) -> Response[ManagedPostgresResize | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresResize | Problem]:
    """Read a company database resize operation.

     Returns progress for a durable resize UUID owned by this database and organization. Reading progress
    does not submit another resize.

    Args:
        organization (str):
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        resize_id=resize_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresResize | Problem | None:
    """Read a company database resize operation.

     Returns progress for a durable resize UUID owned by this database and organization. Reading progress
    does not submit another resize.

    Args:
        organization (str):
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresResize | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        resize_id=resize_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresResize | Problem]:
    """Read a company database resize operation.

     Returns progress for a durable resize UUID owned by this database and organization. Reading progress
    does not submit another resize.

    Args:
        organization (str):
        id (str):
        resize_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        resize_id=resize_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    resize_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresResize | Problem | None:
    """Read a company database resize operation.

     Returns progress for a durable resize UUID owned by this database and organization. Reading progress
    does not submit another resize.

    Args:
        organization (str):
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
            organization=organization,
            id=id,
            resize_id=resize_id,
            client=client,
        )
    ).parsed
