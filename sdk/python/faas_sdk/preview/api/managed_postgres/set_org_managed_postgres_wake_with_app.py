from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_wake_with_app import ManagedPostgresWakeWithApp
from ...models.problem import Problem
from ...models.set_managed_postgres_wake_with_app_request import SetManagedPostgresWakeWithAppRequest
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    *,
    body: SetManagedPostgresWakeWithAppRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/wake-with-app".format(
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
) -> ManagedPostgresWakeWithApp | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresWakeWithApp.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresWakeWithApp | Problem]:
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
    body: SetManagedPostgresWakeWithAppRequest,
) -> Response[ManagedPostgresWakeWithApp | Problem]:
    """Start a company database when its app wakes.

     An owner or admin turns on or off starting a scale-to-zero company database when an app bound to it
    wakes. A wake that never queries the database still starts it.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresWakeWithAppRequest): Whether a scale-to-zero database starts when
            an app bound to it wakes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresWakeWithApp | Problem]
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
    body: SetManagedPostgresWakeWithAppRequest,
) -> ManagedPostgresWakeWithApp | Problem | None:
    """Start a company database when its app wakes.

     An owner or admin turns on or off starting a scale-to-zero company database when an app bound to it
    wakes. A wake that never queries the database still starts it.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresWakeWithAppRequest): Whether a scale-to-zero database starts when
            an app bound to it wakes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresWakeWithApp | Problem
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
    body: SetManagedPostgresWakeWithAppRequest,
) -> Response[ManagedPostgresWakeWithApp | Problem]:
    """Start a company database when its app wakes.

     An owner or admin turns on or off starting a scale-to-zero company database when an app bound to it
    wakes. A wake that never queries the database still starts it.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresWakeWithAppRequest): Whether a scale-to-zero database starts when
            an app bound to it wakes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresWakeWithApp | Problem]
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
    body: SetManagedPostgresWakeWithAppRequest,
) -> ManagedPostgresWakeWithApp | Problem | None:
    """Start a company database when its app wakes.

     An owner or admin turns on or off starting a scale-to-zero company database when an app bound to it
    wakes. A wake that never queries the database still starts it.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresWakeWithAppRequest): Whether a scale-to-zero database starts when
            an app bound to it wakes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresWakeWithApp | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
