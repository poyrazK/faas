from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_capabilities import ManagedPostgresCapabilities
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    *,
    region: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["region"] = region

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/postgres/capabilities".format(
            organization=quote(str(organization), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCapabilities | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresCapabilities.from_dict(response.json())

        return response_200

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresCapabilities | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> Response[ManagedPostgresCapabilities | Problem]:
    """Read PostgreSQL capabilities for the company plan.

     Reports qualified provider capabilities intersected with the organization's plan. Reading
    capabilities does not open the provisioning rollout gate.

    Args:
        organization (str):
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCapabilities | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        region=region,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> ManagedPostgresCapabilities | Problem | None:
    """Read PostgreSQL capabilities for the company plan.

     Reports qualified provider capabilities intersected with the organization's plan. Reading
    capabilities does not open the provisioning rollout gate.

    Args:
        organization (str):
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCapabilities | Problem
    """

    return sync_detailed(
        organization=organization,
        client=client,
        region=region,
    ).parsed


async def asyncio_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> Response[ManagedPostgresCapabilities | Problem]:
    """Read PostgreSQL capabilities for the company plan.

     Reports qualified provider capabilities intersected with the organization's plan. Reading
    capabilities does not open the provisioning rollout gate.

    Args:
        organization (str):
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCapabilities | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        region=region,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> ManagedPostgresCapabilities | Problem | None:
    """Read PostgreSQL capabilities for the company plan.

     Reports qualified provider capabilities intersected with the organization's plan. Reading
    capabilities does not open the provisioning rollout gate.

    Args:
        organization (str):
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCapabilities | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            client=client,
            region=region,
        )
    ).parsed
