from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.get_org_managed_postgres_table_health_sort import (
    GetOrgManagedPostgresTableHealthSort,
)
from ...models.managed_postgres_table_health import ManagedPostgresTableHealth
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    id: str,
    *,
    sort: GetOrgManagedPostgresTableHealthSort | Unset = "size",
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_sort: str | Unset = UNSET
    if not isinstance(sort, Unset):
        json_sort = sort

    params["sort"] = json_sort

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/tables".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresTableHealth | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresTableHealth.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresTableHealth | Problem]:
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
    sort: GetOrgManagedPostgresTableHealthSort | Unset = "size",
    limit: int | Unset = 20,
) -> Response[ManagedPostgresTableHealth | Problem]:
    """Inspect native PostgreSQL table health

     Reads a bounded sample of stored tables and leaf partitions from the running primary. Parked
    databases stay asleep.

    Args:
        organization (str):
        id (str):
        sort (GetOrgManagedPostgresTableHealthSort | Unset):  Default: 'size'.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresTableHealth | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        sort=sort,
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
    sort: GetOrgManagedPostgresTableHealthSort | Unset = "size",
    limit: int | Unset = 20,
) -> ManagedPostgresTableHealth | Problem | None:
    """Inspect native PostgreSQL table health

     Reads a bounded sample of stored tables and leaf partitions from the running primary. Parked
    databases stay asleep.

    Args:
        organization (str):
        id (str):
        sort (GetOrgManagedPostgresTableHealthSort | Unset):  Default: 'size'.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresTableHealth | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        sort=sort,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    sort: GetOrgManagedPostgresTableHealthSort | Unset = "size",
    limit: int | Unset = 20,
) -> Response[ManagedPostgresTableHealth | Problem]:
    """Inspect native PostgreSQL table health

     Reads a bounded sample of stored tables and leaf partitions from the running primary. Parked
    databases stay asleep.

    Args:
        organization (str):
        id (str):
        sort (GetOrgManagedPostgresTableHealthSort | Unset):  Default: 'size'.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresTableHealth | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        sort=sort,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    sort: GetOrgManagedPostgresTableHealthSort | Unset = "size",
    limit: int | Unset = 20,
) -> ManagedPostgresTableHealth | Problem | None:
    """Inspect native PostgreSQL table health

     Reads a bounded sample of stored tables and leaf partitions from the running primary. Parked
    databases stay asleep.

    Args:
        organization (str):
        id (str):
        sort (GetOrgManagedPostgresTableHealthSort | Unset):  Default: 'size'.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresTableHealth | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            sort=sort,
            limit=limit,
        )
    ).parsed
