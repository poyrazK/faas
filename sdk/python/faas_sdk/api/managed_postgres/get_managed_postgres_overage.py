from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_overage import ManagedPostgresOverage
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/overage",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresOverage | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresOverage.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresOverage | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresOverage | Problem]:
    """Show whether compute overage is on

     With overage on, a paid account's managed PostgreSQL databases keep running past the plan's monthly
    compute allowance until the ceiling, twice the allowance, and are then suspended. Off is the
    default; the databases are then suspended at the allowance. A cap never deletes a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresOverage | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresOverage | Problem | None:
    """Show whether compute overage is on

     With overage on, a paid account's managed PostgreSQL databases keep running past the plan's monthly
    compute allowance until the ceiling, twice the allowance, and are then suspended. Off is the
    default; the databases are then suspended at the allowance. A cap never deletes a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresOverage | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresOverage | Problem]:
    """Show whether compute overage is on

     With overage on, a paid account's managed PostgreSQL databases keep running past the plan's monthly
    compute allowance until the ceiling, twice the allowance, and are then suspended. Off is the
    default; the databases are then suspended at the allowance. A cap never deletes a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresOverage | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresOverage | Problem | None:
    """Show whether compute overage is on

     With overage on, a paid account's managed PostgreSQL databases keep running past the plan's monthly
    compute allowance until the ceiling, twice the allowance, and are then suspended. Off is the
    default; the databases are then suspended at the allowance. A cap never deletes a database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresOverage | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
