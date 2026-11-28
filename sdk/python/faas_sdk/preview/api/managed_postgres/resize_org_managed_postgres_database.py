from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_resize import ManagedPostgresResize
from ...models.problem import Problem
from ...models.resize_managed_postgres_database_request import ResizeManagedPostgresDatabaseRequest
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    *,
    body: ResizeManagedPostgresDatabaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/resize".format(
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
) -> ManagedPostgresResize | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresResize.from_dict(response.json())

        return response_202

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
    *,
    client: AuthenticatedClient | Client,
    body: ResizeManagedPostgresDatabaseRequest,
) -> Response[ManagedPostgresResize | Problem]:
    """Change the company database service class.

     An owner or admin submits a durable resize request. The requested class must fit the company plan
    and provider capabilities; connection interruption may occur.

    Args:
        organization (str):
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change on an existing managed PostgreSQL database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
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
    body: ResizeManagedPostgresDatabaseRequest,
) -> ManagedPostgresResize | Problem | None:
    """Change the company database service class.

     An owner or admin submits a durable resize request. The requested class must fit the company plan
    and provider capabilities; connection interruption may occur.

    Args:
        organization (str):
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change on an existing managed PostgreSQL database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresResize | Problem
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
    body: ResizeManagedPostgresDatabaseRequest,
) -> Response[ManagedPostgresResize | Problem]:
    """Change the company database service class.

     An owner or admin submits a durable resize request. The requested class must fit the company plan
    and provider capabilities; connection interruption may occur.

    Args:
        organization (str):
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change on an existing managed PostgreSQL database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
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
    body: ResizeManagedPostgresDatabaseRequest,
) -> ManagedPostgresResize | Problem | None:
    """Change the company database service class.

     An owner or admin submits a durable resize request. The requested class must fit the company plan
    and provider capabilities; connection interruption may occur.

    Args:
        organization (str):
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change on an existing managed PostgreSQL database.

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
            client=client,
            body=body,
        )
    ).parsed
