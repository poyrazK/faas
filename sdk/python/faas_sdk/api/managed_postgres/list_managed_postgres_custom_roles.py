from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_custom_role_list import ManagedPostgresCustomRoleList
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/roles".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCustomRoleList | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresCustomRoleList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresCustomRoleList | Problem]:
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
) -> Response[ManagedPostgresCustomRoleList | Problem]:
    """List a database's custom roles.

     Beta (postgres-custom-roles capability). The
    NOLOGIN group roles declared on the database, with the access modes
    whose logins may SET ROLE to each, the roles each is a member of, the
    access modes whose logins start every session as it, and whether it
    is applied (state ready) yet. Requires postgres read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCustomRoleList | Problem]
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
) -> ManagedPostgresCustomRoleList | Problem | None:
    """List a database's custom roles.

     Beta (postgres-custom-roles capability). The
    NOLOGIN group roles declared on the database, with the access modes
    whose logins may SET ROLE to each, the roles each is a member of, the
    access modes whose logins start every session as it, and whether it
    is applied (state ready) yet. Requires postgres read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCustomRoleList | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresCustomRoleList | Problem]:
    """List a database's custom roles.

     Beta (postgres-custom-roles capability). The
    NOLOGIN group roles declared on the database, with the access modes
    whose logins may SET ROLE to each, the roles each is a member of, the
    access modes whose logins start every session as it, and whether it
    is applied (state ready) yet. Requires postgres read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCustomRoleList | Problem]
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
) -> ManagedPostgresCustomRoleList | Problem | None:
    """List a database's custom roles.

     Beta (postgres-custom-roles capability). The
    NOLOGIN group roles declared on the database, with the access modes
    whose logins may SET ROLE to each, the roles each is a member of, the
    access modes whose logins start every session as it, and whether it
    is applied (state ready) yet. Requires postgres read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCustomRoleList | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
