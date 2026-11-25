from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_error_history import ManagedPostgresErrorHistory
from ...models.problem import Problem
from ...models.set_managed_postgres_error_history_request import SetManagedPostgresErrorHistoryRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: SetManagedPostgresErrorHistoryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/databases/{id}/errors".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresErrorHistory | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresErrorHistory.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresErrorHistory | Problem]:
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
    body: SetManagedPostgresErrorHistoryRequest,
) -> Response[ManagedPostgresErrorHistory | Problem]:
    """Enable or disable collection of pooled PostgreSQL errors.

     Collection defaults to disabled. Disabling collection keeps events already retained until they
    expire.

    Args:
        id (str):
        body (SetManagedPostgresErrorHistoryRequest): Enables or disables recording error metadata
            for pooled connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresErrorHistory | Problem]
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
    body: SetManagedPostgresErrorHistoryRequest,
) -> ManagedPostgresErrorHistory | Problem | None:
    """Enable or disable collection of pooled PostgreSQL errors.

     Collection defaults to disabled. Disabling collection keeps events already retained until they
    expire.

    Args:
        id (str):
        body (SetManagedPostgresErrorHistoryRequest): Enables or disables recording error metadata
            for pooled connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresErrorHistory | Problem
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
    body: SetManagedPostgresErrorHistoryRequest,
) -> Response[ManagedPostgresErrorHistory | Problem]:
    """Enable or disable collection of pooled PostgreSQL errors.

     Collection defaults to disabled. Disabling collection keeps events already retained until they
    expire.

    Args:
        id (str):
        body (SetManagedPostgresErrorHistoryRequest): Enables or disables recording error metadata
            for pooled connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresErrorHistory | Problem]
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
    body: SetManagedPostgresErrorHistoryRequest,
) -> ManagedPostgresErrorHistory | Problem | None:
    """Enable or disable collection of pooled PostgreSQL errors.

     Collection defaults to disabled. Disabling collection keeps events already retained until they
    expire.

    Args:
        id (str):
        body (SetManagedPostgresErrorHistoryRequest): Enables or disables recording error metadata
            for pooled connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresErrorHistory | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
