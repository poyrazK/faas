from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_migration_progress import ManagedPostgresMigrationProgress
from ...models.managed_postgres_migration_verify_request import ManagedPostgresMigrationVerifyRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ManagedPostgresMigrationVerifyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/migration/verify".format(
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresMigrationVerifyRequest,
) -> Response[ManagedPostgresMigrationProgress | Problem]:
    """Verify durable replay after a PostgreSQL migration source write pause

     Queues a replicated marker and sequence synchronization after an operator confirms a source write
    pause. Read the receipt from migration status. Keep source writes paused throughout verification and
    until activation.

    Args:
        id (str):
        body (ManagedPostgresMigrationVerifyRequest): Request to verify migration replay after
            source writes have paused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMigrationProgress | Problem]
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
    body: ManagedPostgresMigrationVerifyRequest,
) -> ManagedPostgresMigrationProgress | Problem | None:
    """Verify durable replay after a PostgreSQL migration source write pause

     Queues a replicated marker and sequence synchronization after an operator confirms a source write
    pause. Read the receipt from migration status. Keep source writes paused throughout verification and
    until activation.

    Args:
        id (str):
        body (ManagedPostgresMigrationVerifyRequest): Request to verify migration replay after
            source writes have paused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMigrationProgress | Problem
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
    body: ManagedPostgresMigrationVerifyRequest,
) -> Response[ManagedPostgresMigrationProgress | Problem]:
    """Verify durable replay after a PostgreSQL migration source write pause

     Queues a replicated marker and sequence synchronization after an operator confirms a source write
    pause. Read the receipt from migration status. Keep source writes paused throughout verification and
    until activation.

    Args:
        id (str):
        body (ManagedPostgresMigrationVerifyRequest): Request to verify migration replay after
            source writes have paused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMigrationProgress | Problem]
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
    body: ManagedPostgresMigrationVerifyRequest,
) -> ManagedPostgresMigrationProgress | Problem | None:
    """Verify durable replay after a PostgreSQL migration source write pause

     Queues a replicated marker and sequence synchronization after an operator confirms a source write
    pause. Read the receipt from migration status. Keep source writes paused throughout verification and
    until activation.

    Args:
        id (str):
        body (ManagedPostgresMigrationVerifyRequest): Request to verify migration replay after
            source writes have paused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMigrationProgress | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
