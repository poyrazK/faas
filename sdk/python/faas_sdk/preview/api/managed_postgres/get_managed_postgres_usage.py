from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_usage_response import ManagedPostgresUsageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/managed-postgres-usage",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresUsageResponse | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresUsageResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresUsageResponse | Problem]:
    """Read managed PostgreSQL usage and guardrail state

     Requires usage read scope. Returns normalized current-month meters,
    plan headroom, and freshness state. Provider IDs, rates, credentials,
    and internal cost line items are never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUsageResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresUsageResponse | Problem | None:
    """Read managed PostgreSQL usage and guardrail state

     Requires usage read scope. Returns normalized current-month meters,
    plan headroom, and freshness state. Provider IDs, rates, credentials,
    and internal cost line items are never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUsageResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresUsageResponse | Problem]:
    """Read managed PostgreSQL usage and guardrail state

     Requires usage read scope. Returns normalized current-month meters,
    plan headroom, and freshness state. Provider IDs, rates, credentials,
    and internal cost line items are never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUsageResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresUsageResponse | Problem | None:
    """Read managed PostgreSQL usage and guardrail state

     Requires usage read scope. Returns normalized current-month meters,
    plan headroom, and freshness state. Provider IDs, rates, credentials,
    and internal cost line items are never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
