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
    id: str,
    *,
    body: ResizeManagedPostgresDatabaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/resize".format(
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
    *,
    client: AuthenticatedClient | Client,
    body: ResizeManagedPostgresDatabaseRequest,
) -> Response[ManagedPostgresResize | Problem]:
    """Resize managed PostgreSQL compute or storage

     Durably reserves a service-class change, a storage-limit increase, or both, on the pinned dataset.
    Clients may disconnect during resizing. The request_id UUID is the durable idempotency key; repeat
    the same UUID and target to recover progress. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. Existing accepted requests remain replayable after admission
    closes. Storage only grows; a lower limit is rejected, and a limit above the plan's per-database
    allowance fails with limit, observed and docs_url. Only class and storage limit change; ready
    confirms provider observation, not uninterrupted connections. Published environment-clone targets
    and databases without a pinned data identity are currently unsupported.

    Args:
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change, a storage-limit increase, or both, on an existing managed PostgreSQL database. At
            least one of service_class and storage_limit_bytes is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
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
    body: ResizeManagedPostgresDatabaseRequest,
) -> ManagedPostgresResize | Problem | None:
    """Resize managed PostgreSQL compute or storage

     Durably reserves a service-class change, a storage-limit increase, or both, on the pinned dataset.
    Clients may disconnect during resizing. The request_id UUID is the durable idempotency key; repeat
    the same UUID and target to recover progress. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. Existing accepted requests remain replayable after admission
    closes. Storage only grows; a lower limit is rejected, and a limit above the plan's per-database
    allowance fails with limit, observed and docs_url. Only class and storage limit change; ready
    confirms provider observation, not uninterrupted connections. Published environment-clone targets
    and databases without a pinned data identity are currently unsupported.

    Args:
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change, a storage-limit increase, or both, on an existing managed PostgreSQL database. At
            least one of service_class and storage_limit_bytes is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresResize | Problem
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
    body: ResizeManagedPostgresDatabaseRequest,
) -> Response[ManagedPostgresResize | Problem]:
    """Resize managed PostgreSQL compute or storage

     Durably reserves a service-class change, a storage-limit increase, or both, on the pinned dataset.
    Clients may disconnect during resizing. The request_id UUID is the durable idempotency key; repeat
    the same UUID and target to recover progress. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. Existing accepted requests remain replayable after admission
    closes. Storage only grows; a lower limit is rejected, and a limit above the plan's per-database
    allowance fails with limit, observed and docs_url. Only class and storage limit change; ready
    confirms provider observation, not uninterrupted connections. Published environment-clone targets
    and databases without a pinned data identity are currently unsupported.

    Args:
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change, a storage-limit increase, or both, on an existing managed PostgreSQL database. At
            least one of service_class and storage_limit_bytes is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresResize | Problem]
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
    body: ResizeManagedPostgresDatabaseRequest,
) -> ManagedPostgresResize | Problem | None:
    """Resize managed PostgreSQL compute or storage

     Durably reserves a service-class change, a storage-limit increase, or both, on the pinned dataset.
    Clients may disconnect during resizing. The request_id UUID is the durable idempotency key; repeat
    the same UUID and target to recover progress. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. Existing accepted requests remain replayable after admission
    closes. Storage only grows; a lower limit is rejected, and a limit above the plan's per-database
    allowance fails with limit, observed and docs_url. Only class and storage limit change; ready
    confirms provider observation, not uninterrupted connections. Published environment-clone targets
    and databases without a pinned data identity are currently unsupported.

    Args:
        id (str):
        body (ResizeManagedPostgresDatabaseRequest): Canonical UUID request for a compute-class
            change, a storage-limit increase, or both, on an existing managed PostgreSQL database. At
            least one of service_class and storage_limit_bytes is required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresResize | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
