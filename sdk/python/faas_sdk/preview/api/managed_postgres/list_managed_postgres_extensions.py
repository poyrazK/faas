from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_extensions import ManagedPostgresExtensions
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    target_major: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["target_major"] = target_major

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/extensions".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresExtensions | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresExtensions.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresExtensions | Problem]:
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
    target_major: int | Unset = UNSET,
) -> Response[ManagedPostgresExtensions | Problem]:
    """List native PostgreSQL extension versions

     Reads the current primary and may wake it. An optional later major compares installed versions with
    the configured upgrade image.

    Args:
        id (str):
        target_major (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresExtensions | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        target_major=target_major,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    target_major: int | Unset = UNSET,
) -> ManagedPostgresExtensions | Problem | None:
    """List native PostgreSQL extension versions

     Reads the current primary and may wake it. An optional later major compares installed versions with
    the configured upgrade image.

    Args:
        id (str):
        target_major (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresExtensions | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        target_major=target_major,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    target_major: int | Unset = UNSET,
) -> Response[ManagedPostgresExtensions | Problem]:
    """List native PostgreSQL extension versions

     Reads the current primary and may wake it. An optional later major compares installed versions with
    the configured upgrade image.

    Args:
        id (str):
        target_major (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresExtensions | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        target_major=target_major,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    target_major: int | Unset = UNSET,
) -> ManagedPostgresExtensions | Problem | None:
    """List native PostgreSQL extension versions

     Reads the current primary and may wake it. An optional later major compares installed versions with
    the configured upgrade image.

    Args:
        id (str):
        target_major (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresExtensions | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            target_major=target_major,
        )
    ).parsed
