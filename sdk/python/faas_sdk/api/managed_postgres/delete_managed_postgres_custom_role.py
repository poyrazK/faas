from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_custom_role import ManagedPostgresCustomRole
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    name: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/postgres/databases/{id}/roles/{name}".format(
            id=quote(str(id), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCustomRole | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresCustomRole.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresCustomRole | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresCustomRole | Problem]:
    """Drop a custom role.

     Beta (postgres-custom-roles capability). Marks the role deleting;
    the platform then hands what it owns to the schema owner, removes its
    grants and drops it. Requires postgres manage scope and MFA.

    Args:
        id (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCustomRole | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        name=name,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresCustomRole | Problem | None:
    """Drop a custom role.

     Beta (postgres-custom-roles capability). Marks the role deleting;
    the platform then hands what it owns to the schema owner, removes its
    grants and drops it. Requires postgres manage scope and MFA.

    Args:
        id (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCustomRole | Problem
    """

    return sync_detailed(
        id=id,
        name=name,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresCustomRole | Problem]:
    """Drop a custom role.

     Beta (postgres-custom-roles capability). Marks the role deleting;
    the platform then hands what it owns to the schema owner, removes its
    grants and drops it. Requires postgres manage scope and MFA.

    Args:
        id (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCustomRole | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresCustomRole | Problem | None:
    """Drop a custom role.

     Beta (postgres-custom-roles capability). Marks the role deleting;
    the platform then hands what it owns to the schema owner, removes its
    grants and drops it. Requires postgres manage scope and MFA.

    Args:
        id (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCustomRole | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            name=name,
            client=client,
        )
    ).parsed
