from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_live_queries import ManagedPostgresLiveQueries
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/live".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresLiveQueries | Problem:
    if response.status_code == 201:
        response_201 = ManagedPostgresLiveQueries.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresLiveQueries | Problem]:
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
) -> Response[ManagedPostgresLiveQueries | Problem]:
    """Enable managed PostgreSQL live queries

     Enables live queries on a native database and issues its live secret, which backends seal queries
    with. The secret is returned once, in this response; it is stored sealed to the live-query service
    and cannot be read again. A database that already has live queries conflicts; rotate its secret
    instead. Operator preview.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresLiveQueries | Problem]
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
) -> ManagedPostgresLiveQueries | Problem | None:
    """Enable managed PostgreSQL live queries

     Enables live queries on a native database and issues its live secret, which backends seal queries
    with. The secret is returned once, in this response; it is stored sealed to the live-query service
    and cannot be read again. A database that already has live queries conflicts; rotate its secret
    instead. Operator preview.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresLiveQueries | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresLiveQueries | Problem]:
    """Enable managed PostgreSQL live queries

     Enables live queries on a native database and issues its live secret, which backends seal queries
    with. The secret is returned once, in this response; it is stored sealed to the live-query service
    and cannot be read again. A database that already has live queries conflicts; rotate its secret
    instead. Operator preview.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresLiveQueries | Problem]
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
) -> ManagedPostgresLiveQueries | Problem | None:
    """Enable managed PostgreSQL live queries

     Enables live queries on a native database and issues its live secret, which backends seal queries
    with. The secret is returned once, in this response; it is stored sealed to the live-query service
    and cannot be read again. A database that already has live queries conflicts; rotate its secret
    instead. Operator preview.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresLiveQueries | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
