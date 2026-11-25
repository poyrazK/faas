from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_restore_drill import ManagedPostgresRestoreDrill
from ...models.problem import Problem
from ...models.request_managed_postgres_restore_drill import RequestManagedPostgresRestoreDrill
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    *,
    body: RequestManagedPostgresRestoreDrill,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/restore-drills".format(
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
) -> ManagedPostgresRestoreDrill | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresRestoreDrill.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresRestoreDrill | Problem]:
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
    body: RequestManagedPostgresRestoreDrill,
) -> Response[ManagedPostgresRestoreDrill | Problem]:
    """Queue a native PostgreSQL restore drill

     Saves an idempotent request for a temporary restore and may wake the source to write a verification
    marker.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresRestoreDrill):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresRestoreDrill | Problem]
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
    body: RequestManagedPostgresRestoreDrill,
) -> ManagedPostgresRestoreDrill | Problem | None:
    """Queue a native PostgreSQL restore drill

     Saves an idempotent request for a temporary restore and may wake the source to write a verification
    marker.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresRestoreDrill):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresRestoreDrill | Problem
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
    body: RequestManagedPostgresRestoreDrill,
) -> Response[ManagedPostgresRestoreDrill | Problem]:
    """Queue a native PostgreSQL restore drill

     Saves an idempotent request for a temporary restore and may wake the source to write a verification
    marker.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresRestoreDrill):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresRestoreDrill | Problem]
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
    body: RequestManagedPostgresRestoreDrill,
) -> ManagedPostgresRestoreDrill | Problem | None:
    """Queue a native PostgreSQL restore drill

     Saves an idempotent request for a temporary restore and may wake the source to write a verification
    marker.

    Args:
        organization (str):
        id (str):
        body (RequestManagedPostgresRestoreDrill):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresRestoreDrill | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
