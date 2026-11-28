from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_database import ManagedPostgresDatabase
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    purge: bool | Unset = False,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    params["purge"] = purge

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/postgres/databases/{id}".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresDatabase | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresDatabase.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresDatabase | Problem]:
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
    purge: bool | Unset = False,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresDatabase | Problem]:
    """Delete a managed PostgreSQL database

     A ready database is kept for 7 days with its compute stopped and its connections refused, and can be
    undeleted until restorable_until. It still counts toward the plan's database limit and keeps its
    name until it is purged. Any other database, or a delete with purge=true, is deleted at once.
    Bindings and restore dependencies must be removed first.

    Args:
        id (str):
        purge (bool | Unset):  Default: False.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresDatabase | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        purge=purge,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    purge: bool | Unset = False,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresDatabase | Problem | None:
    """Delete a managed PostgreSQL database

     A ready database is kept for 7 days with its compute stopped and its connections refused, and can be
    undeleted until restorable_until. It still counts toward the plan's database limit and keeps its
    name until it is purged. Any other database, or a delete with purge=true, is deleted at once.
    Bindings and restore dependencies must be removed first.

    Args:
        id (str):
        purge (bool | Unset):  Default: False.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresDatabase | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        purge=purge,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    purge: bool | Unset = False,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresDatabase | Problem]:
    """Delete a managed PostgreSQL database

     A ready database is kept for 7 days with its compute stopped and its connections refused, and can be
    undeleted until restorable_until. It still counts toward the plan's database limit and keeps its
    name until it is purged. Any other database, or a delete with purge=true, is deleted at once.
    Bindings and restore dependencies must be removed first.

    Args:
        id (str):
        purge (bool | Unset):  Default: False.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresDatabase | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        purge=purge,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    purge: bool | Unset = False,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresDatabase | Problem | None:
    """Delete a managed PostgreSQL database

     A ready database is kept for 7 days with its compute stopped and its connections refused, and can be
    undeleted until restorable_until. It still counts toward the plan's database limit and keeps its
    name until it is purged. Any other database, or a delete with purge=true, is deleted at once.
    Bindings and restore dependencies must be removed first.

    Args:
        id (str):
        purge (bool | Unset):  Default: False.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresDatabase | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            purge=purge,
            idempotency_key=idempotency_key,
        )
    ).parsed
