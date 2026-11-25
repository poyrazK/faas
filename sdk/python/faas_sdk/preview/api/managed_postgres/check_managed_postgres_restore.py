import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_restore_check import ManagedPostgresRestoreCheck
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    id: str,
    *,
    point_in_time: datetime.datetime,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_point_in_time = point_in_time.isoformat()
    params["point_in_time"] = json_point_in_time

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/restore-check".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresRestoreCheck | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresRestoreCheck.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresRestoreCheck | Problem]:
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
    point_in_time: datetime.datetime,
) -> Response[ManagedPostgresRestoreCheck | Problem]:
    """Check native PostgreSQL archive evidence for a restore target.

     Reads the selected base backup and bounded WAL evidence for this target. A covered result describes
    the archive at check time.

    Args:
        id (str):
        point_in_time (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresRestoreCheck | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        point_in_time=point_in_time,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    point_in_time: datetime.datetime,
) -> ManagedPostgresRestoreCheck | Problem | None:
    """Check native PostgreSQL archive evidence for a restore target.

     Reads the selected base backup and bounded WAL evidence for this target. A covered result describes
    the archive at check time.

    Args:
        id (str):
        point_in_time (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresRestoreCheck | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        point_in_time=point_in_time,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    point_in_time: datetime.datetime,
) -> Response[ManagedPostgresRestoreCheck | Problem]:
    """Check native PostgreSQL archive evidence for a restore target.

     Reads the selected base backup and bounded WAL evidence for this target. A covered result describes
    the archive at check time.

    Args:
        id (str):
        point_in_time (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresRestoreCheck | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        point_in_time=point_in_time,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    point_in_time: datetime.datetime,
) -> ManagedPostgresRestoreCheck | Problem | None:
    """Check native PostgreSQL archive evidence for a restore target.

     Reads the selected base backup and bounded WAL evidence for this target. A covered result describes
    the archive at check time.

    Args:
        id (str):
        point_in_time (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresRestoreCheck | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            point_in_time=point_in_time,
        )
    ).parsed
