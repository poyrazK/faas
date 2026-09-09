from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_usage_operator_response import ManagedPostgresUsageOperatorResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    account_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/managed-postgres/usage/{account_id}".format(
            account_id=quote(str(account_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresUsageOperatorResponse | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresUsageOperatorResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresUsageOperatorResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresUsageOperatorResponse | Problem]:
    """Read normalized managed PostgreSQL usage for an account

     Operator-only view with effective guardrail ceilings and internal COGS line items. Provider IDs and
    credentials are never returned.

    Args:
        account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUsageOperatorResponse | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresUsageOperatorResponse | Problem | None:
    """Read normalized managed PostgreSQL usage for an account

     Operator-only view with effective guardrail ceilings and internal COGS line items. Provider IDs and
    credentials are never returned.

    Args:
        account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUsageOperatorResponse | Problem
    """

    return sync_detailed(
        account_id=account_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresUsageOperatorResponse | Problem]:
    """Read normalized managed PostgreSQL usage for an account

     Operator-only view with effective guardrail ceilings and internal COGS line items. Provider IDs and
    credentials are never returned.

    Args:
        account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUsageOperatorResponse | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresUsageOperatorResponse | Problem | None:
    """Read normalized managed PostgreSQL usage for an account

     Operator-only view with effective guardrail ceilings and internal COGS line items. Provider IDs and
    credentials are never returned.

    Args:
        account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUsageOperatorResponse | Problem
    """

    return (
        await asyncio_detailed(
            account_id=account_id,
            client=client,
        )
    ).parsed
