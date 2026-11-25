from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_error_history import ManagedPostgresErrorHistory
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    id: str,
    *,
    since_seconds: int | Unset = 3600,
    sqlstate: str | Unset = UNSET,
    limit: int | Unset = 40,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since_seconds"] = since_seconds

    params["sqlstate"] = sqlstate

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/errors".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

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
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since_seconds: int | Unset = 3600,
    sqlstate: str | Unset = UNSET,
    limit: int | Unset = 40,
) -> Response[ManagedPostgresErrorHistory | Problem]:
    """Read recent errors from native PostgreSQL pooled connections for the organization.

     On the organization's database, returns newest first. The history keeps up to 1000 events per
    database for up to 24 hours. Reading history does not wake a parked database.

    Args:
        organization (str):
        id (str):
        since_seconds (int | Unset):  Default: 3600.
        sqlstate (str | Unset):
        limit (int | Unset):  Default: 40.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresErrorHistory | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        since_seconds=since_seconds,
        sqlstate=sqlstate,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since_seconds: int | Unset = 3600,
    sqlstate: str | Unset = UNSET,
    limit: int | Unset = 40,
) -> ManagedPostgresErrorHistory | Problem | None:
    """Read recent errors from native PostgreSQL pooled connections for the organization.

     On the organization's database, returns newest first. The history keeps up to 1000 events per
    database for up to 24 hours. Reading history does not wake a parked database.

    Args:
        organization (str):
        id (str):
        since_seconds (int | Unset):  Default: 3600.
        sqlstate (str | Unset):
        limit (int | Unset):  Default: 40.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresErrorHistory | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        since_seconds=since_seconds,
        sqlstate=sqlstate,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since_seconds: int | Unset = 3600,
    sqlstate: str | Unset = UNSET,
    limit: int | Unset = 40,
) -> Response[ManagedPostgresErrorHistory | Problem]:
    """Read recent errors from native PostgreSQL pooled connections for the organization.

     On the organization's database, returns newest first. The history keeps up to 1000 events per
    database for up to 24 hours. Reading history does not wake a parked database.

    Args:
        organization (str):
        id (str):
        since_seconds (int | Unset):  Default: 3600.
        sqlstate (str | Unset):
        limit (int | Unset):  Default: 40.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresErrorHistory | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        since_seconds=since_seconds,
        sqlstate=sqlstate,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since_seconds: int | Unset = 3600,
    sqlstate: str | Unset = UNSET,
    limit: int | Unset = 40,
) -> ManagedPostgresErrorHistory | Problem | None:
    """Read recent errors from native PostgreSQL pooled connections for the organization.

     On the organization's database, returns newest first. The history keeps up to 1000 events per
    database for up to 24 hours. Reading history does not wake a parked database.

    Args:
        organization (str):
        id (str):
        since_seconds (int | Unset):  Default: 3600.
        sqlstate (str | Unset):
        limit (int | Unset):  Default: 40.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresErrorHistory | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            since_seconds=since_seconds,
            sqlstate=sqlstate,
            limit=limit,
        )
    ).parsed
