from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_custom_role import ManagedPostgresCustomRole
from ...models.problem import Problem
from ...models.put_managed_postgres_custom_role_request import PutManagedPostgresCustomRoleRequest
from ...types import Response


def _get_kwargs(
    id: str,
    name: str,
    *,
    body: PutManagedPostgresCustomRoleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/databases/{id}/roles/{name}".format(
            id=quote(str(id), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCustomRole | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresCustomRole.from_dict(response.json())

        return response_200

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
    body: PutManagedPostgresCustomRoleRequest,
) -> Response[ManagedPostgresCustomRole | Problem]:
    """Declare or replace a custom role.

     Beta (postgres-custom-roles capability, ADR-20261116213000). Creates
    the NOLOGIN role on the database, or corrects it to this declaration,
    and grants it, SET only, to the database's logins of the granted
    access modes, including logins issued later. The role and the
    read_write logins can run the schema owner's functions in public
    (ADR-20261123112326). bypass_rls is refused on a database with Data
    API or Auth. Hobby and above; at most 16 roles per database. Requires
    postgres manage scope, MFA and a verified email. Answers 200 when the
    role is already applied and 202 while it is being applied.

    Args:
        id (str):
        name (str):
        body (PutManagedPostgresCustomRoleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCustomRole | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        name=name,
        body=body,
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
    body: PutManagedPostgresCustomRoleRequest,
) -> ManagedPostgresCustomRole | Problem | None:
    """Declare or replace a custom role.

     Beta (postgres-custom-roles capability, ADR-20261116213000). Creates
    the NOLOGIN role on the database, or corrects it to this declaration,
    and grants it, SET only, to the database's logins of the granted
    access modes, including logins issued later. The role and the
    read_write logins can run the schema owner's functions in public
    (ADR-20261123112326). bypass_rls is refused on a database with Data
    API or Auth. Hobby and above; at most 16 roles per database. Requires
    postgres manage scope, MFA and a verified email. Answers 200 when the
    role is already applied and 202 while it is being applied.

    Args:
        id (str):
        name (str):
        body (PutManagedPostgresCustomRoleRequest):

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
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutManagedPostgresCustomRoleRequest,
) -> Response[ManagedPostgresCustomRole | Problem]:
    """Declare or replace a custom role.

     Beta (postgres-custom-roles capability, ADR-20261116213000). Creates
    the NOLOGIN role on the database, or corrects it to this declaration,
    and grants it, SET only, to the database's logins of the granted
    access modes, including logins issued later. The role and the
    read_write logins can run the schema owner's functions in public
    (ADR-20261123112326). bypass_rls is refused on a database with Data
    API or Auth. Hobby and above; at most 16 roles per database. Requires
    postgres manage scope, MFA and a verified email. Answers 200 when the
    role is already applied and 202 while it is being applied.

    Args:
        id (str):
        name (str):
        body (PutManagedPostgresCustomRoleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCustomRole | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutManagedPostgresCustomRoleRequest,
) -> ManagedPostgresCustomRole | Problem | None:
    """Declare or replace a custom role.

     Beta (postgres-custom-roles capability, ADR-20261116213000). Creates
    the NOLOGIN role on the database, or corrects it to this declaration,
    and grants it, SET only, to the database's logins of the granted
    access modes, including logins issued later. The role and the
    read_write logins can run the schema owner's functions in public
    (ADR-20261123112326). bypass_rls is refused on a database with Data
    API or Auth. Hobby and above; at most 16 roles per database. Requires
    postgres manage scope, MFA and a verified email. Answers 200 when the
    role is already applied and 202 while it is being applied.

    Args:
        id (str):
        name (str):
        body (PutManagedPostgresCustomRoleRequest):

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
            body=body,
        )
    ).parsed
