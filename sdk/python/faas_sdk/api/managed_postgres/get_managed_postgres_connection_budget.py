from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_connection_budget import ManagedPostgresConnectionBudget
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/connection-budget".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresConnectionBudget | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresConnectionBudget.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresConnectionBudget | Problem]:
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
) -> Response[ManagedPostgresConnectionBudget | Problem]:
    """Inspect read-only database connection planning

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresConnectionBudget | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresConnectionBudget | Problem | None:
    """Inspect read-only database connection planning

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresConnectionBudget | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresConnectionBudget | Problem]:
    """Inspect read-only database connection planning

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresConnectionBudget | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresConnectionBudget | Problem | None:
    """Inspect read-only database connection planning

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresConnectionBudget | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
