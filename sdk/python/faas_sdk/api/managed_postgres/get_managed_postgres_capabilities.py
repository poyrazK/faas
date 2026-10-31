from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_capabilities import ManagedPostgresCapabilities
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    region: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["region"] = region

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/capabilities",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCapabilities | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresCapabilities.from_dict(response.json())

        return response_200

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
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> Response[ManagedPostgresCapabilities | Problem]:
    """Show plan and region PostgreSQL feature support

     Returns configured provider-neutral support after plan limits without provider calls.
    provisioning_enabled includes qualification and canary gates. Current usage, budget, and quota
    admission are checked separately at reservation. Existing databases remain pinned to their original
    backend.

    Args:
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCapabilities | Problem]
    """

    kwargs = _get_kwargs(
        region=region,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> ManagedPostgresCapabilities | Problem | None:
    """Show plan and region PostgreSQL feature support

     Returns configured provider-neutral support after plan limits without provider calls.
    provisioning_enabled includes qualification and canary gates. Current usage, budget, and quota
    admission are checked separately at reservation. Existing databases remain pinned to their original
    backend.

    Args:
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCapabilities | Problem
    """

    return sync_detailed(
        client=client,
        region=region,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> Response[ManagedPostgresCapabilities | Problem]:
    """Show plan and region PostgreSQL feature support

     Returns configured provider-neutral support after plan limits without provider calls.
    provisioning_enabled includes qualification and canary gates. Current usage, budget, and quota
    admission are checked separately at reservation. Existing databases remain pinned to their original
    backend.

    Args:
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCapabilities | Problem]
    """

    kwargs = _get_kwargs(
        region=region,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    region: str | Unset = UNSET,
) -> ManagedPostgresCapabilities | Problem | None:
    """Show plan and region PostgreSQL feature support

     Returns configured provider-neutral support after plan limits without provider calls.
    provisioning_enabled includes qualification and canary gates. Current usage, budget, and quota
    admission are checked separately at reservation. Existing databases remain pinned to their original
    backend.

    Args:
        region (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCapabilities | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            region=region,
        )
    ).parsed
