from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_activity import ManagedPostgresActivity
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    id: str,
    *,
    since: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/activity".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresActivity | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresActivity.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresActivity | Problem]:
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
    since: str | Unset = UNSET,
) -> Response[ManagedPostgresActivity | Problem]:
    """Show what kept a company database awake.

     A member who can read the organization's databases lists a company database's recorded compute
    starts with their likely causes, each cause's awake time, and hints.

    Args:
        organization (str):
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresActivity | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        since=since,
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
    since: str | Unset = UNSET,
) -> ManagedPostgresActivity | Problem | None:
    """Show what kept a company database awake.

     A member who can read the organization's databases lists a company database's recorded compute
    starts with their likely causes, each cause's awake time, and hints.

    Args:
        organization (str):
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresActivity | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        since=since,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
) -> Response[ManagedPostgresActivity | Problem]:
    """Show what kept a company database awake.

     A member who can read the organization's databases lists a company database's recorded compute
    starts with their likely causes, each cause's awake time, and hints.

    Args:
        organization (str):
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresActivity | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
) -> ManagedPostgresActivity | Problem | None:
    """Show what kept a company database awake.

     A member who can read the organization's databases lists a company database's recorded compute
    starts with their likely causes, each cause's awake time, and hints.

    Args:
        organization (str):
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresActivity | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            since=since,
        )
    ).parsed
