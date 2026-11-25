from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_autovacuum import ManagedPostgresAutovacuum
from ...models.problem import Problem
from ...models.set_managed_postgres_autovacuum_request import SetManagedPostgresAutovacuumRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: SetManagedPostgresAutovacuumRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/databases/{id}/autovacuum".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresAutovacuum | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresAutovacuum.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresAutovacuum | Problem]:
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
    body: SetManagedPostgresAutovacuumRequest,
) -> Response[ManagedPostgresAutovacuum | Problem]:
    """Set a native PostgreSQL autovacuum profile

     Saves the requested profile. Gregale reloads running instances and applies the saved profile at the
    next wake on parked databases.

    Args:
        id (str):
        body (SetManagedPostgresAutovacuumRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAutovacuum | Problem]
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
    body: SetManagedPostgresAutovacuumRequest,
) -> ManagedPostgresAutovacuum | Problem | None:
    """Set a native PostgreSQL autovacuum profile

     Saves the requested profile. Gregale reloads running instances and applies the saved profile at the
    next wake on parked databases.

    Args:
        id (str):
        body (SetManagedPostgresAutovacuumRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAutovacuum | Problem
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
    body: SetManagedPostgresAutovacuumRequest,
) -> Response[ManagedPostgresAutovacuum | Problem]:
    """Set a native PostgreSQL autovacuum profile

     Saves the requested profile. Gregale reloads running instances and applies the saved profile at the
    next wake on parked databases.

    Args:
        id (str):
        body (SetManagedPostgresAutovacuumRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAutovacuum | Problem]
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
    body: SetManagedPostgresAutovacuumRequest,
) -> ManagedPostgresAutovacuum | Problem | None:
    """Set a native PostgreSQL autovacuum profile

     Saves the requested profile. Gregale reloads running instances and applies the saved profile at the
    next wake on parked databases.

    Args:
        id (str):
        body (SetManagedPostgresAutovacuumRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAutovacuum | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
