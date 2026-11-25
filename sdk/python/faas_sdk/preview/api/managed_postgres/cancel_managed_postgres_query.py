from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.cancel_managed_postgres_query_request import CancelManagedPostgresQueryRequest
from ...models.managed_postgres_query_cancellation import ManagedPostgresQueryCancellation
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: CancelManagedPostgresQueryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/cancel-query".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresQueryCancellation | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresQueryCancellation.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresQueryCancellation | Problem]:
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
    body: CancelManagedPostgresQueryRequest,
) -> Response[ManagedPostgresQueryCancellation | Problem]:
    """Send a cancel request for a live PostgreSQL query target

     Sends a PostgreSQL cancel request through the proxy that owns the execution. The target must come
    from a recent session sample. Replaying the same target returns its recorded outcome while the
    receipt is retained.

    Args:
        id (str):
        body (CancelManagedPostgresQueryRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryCancellation | Problem]
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
    body: CancelManagedPostgresQueryRequest,
) -> ManagedPostgresQueryCancellation | Problem | None:
    """Send a cancel request for a live PostgreSQL query target

     Sends a PostgreSQL cancel request through the proxy that owns the execution. The target must come
    from a recent session sample. Replaying the same target returns its recorded outcome while the
    receipt is retained.

    Args:
        id (str):
        body (CancelManagedPostgresQueryRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryCancellation | Problem
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
    body: CancelManagedPostgresQueryRequest,
) -> Response[ManagedPostgresQueryCancellation | Problem]:
    """Send a cancel request for a live PostgreSQL query target

     Sends a PostgreSQL cancel request through the proxy that owns the execution. The target must come
    from a recent session sample. Replaying the same target returns its recorded outcome while the
    receipt is retained.

    Args:
        id (str):
        body (CancelManagedPostgresQueryRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryCancellation | Problem]
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
    body: CancelManagedPostgresQueryRequest,
) -> ManagedPostgresQueryCancellation | Problem | None:
    """Send a cancel request for a live PostgreSQL query target

     Sends a PostgreSQL cancel request through the proxy that owns the execution. The target must come
    from a recent session sample. Replaying the same target returns its recorded outcome while the
    receipt is retained.

    Args:
        id (str):
        body (CancelManagedPostgresQueryRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryCancellation | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
