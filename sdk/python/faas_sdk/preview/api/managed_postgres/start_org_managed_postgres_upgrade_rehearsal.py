from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_upgrade_rehearsal import ManagedPostgresUpgradeRehearsal
from ...models.problem import Problem
from ...models.request_managed_postgres_upgrade_rehearsal import RequestManagedPostgresUpgradeRehearsal
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    *,
    body: RequestManagedPostgresUpgradeRehearsal,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/upgrade-rehearsals".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresUpgradeRehearsal | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresUpgradeRehearsal.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresUpgradeRehearsal | Problem]:
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
    body: RequestManagedPostgresUpgradeRehearsal,
) -> Response[ManagedPostgresUpgradeRehearsal | Problem]:
    """Queue a native PostgreSQL upgrade rehearsal for the organization.

     On the organization's database, queues a rehearsal of the target major on a private restore from
    verified archive evidence. The source keeps its current version and runtime state.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresUpgradeRehearsal): Target PostgreSQL major and request
            identifier for a private upgrade rehearsal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUpgradeRehearsal | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
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
    body: RequestManagedPostgresUpgradeRehearsal,
) -> ManagedPostgresUpgradeRehearsal | Problem | None:
    """Queue a native PostgreSQL upgrade rehearsal for the organization.

     On the organization's database, queues a rehearsal of the target major on a private restore from
    verified archive evidence. The source keeps its current version and runtime state.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresUpgradeRehearsal): Target PostgreSQL major and request
            identifier for a private upgrade rehearsal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUpgradeRehearsal | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RequestManagedPostgresUpgradeRehearsal,
) -> Response[ManagedPostgresUpgradeRehearsal | Problem]:
    """Queue a native PostgreSQL upgrade rehearsal for the organization.

     On the organization's database, queues a rehearsal of the target major on a private restore from
    verified archive evidence. The source keeps its current version and runtime state.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresUpgradeRehearsal): Target PostgreSQL major and request
            identifier for a private upgrade rehearsal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUpgradeRehearsal | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RequestManagedPostgresUpgradeRehearsal,
) -> ManagedPostgresUpgradeRehearsal | Problem | None:
    """Queue a native PostgreSQL upgrade rehearsal for the organization.

     On the organization's database, queues a rehearsal of the target major on a private restore from
    verified archive evidence. The source keeps its current version and runtime state.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresUpgradeRehearsal): Target PostgreSQL major and request
            identifier for a private upgrade rehearsal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUpgradeRehearsal | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
