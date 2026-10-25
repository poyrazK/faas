from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_connection_response import BillingConnectionResponse
from ...models.billing_provider import BillingProvider
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    provider: BillingProvider,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/billing-connections/{provider}".format(
            provider=quote(str(provider), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingConnectionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingConnectionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingConnectionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingConnectionResponse | Problem]:
    """Describe the account's key for one billing provider.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingConnectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
) -> BillingConnectionResponse | Problem | None:
    """Describe the account's key for one billing provider.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingConnectionResponse | Problem
    """

    return sync_detailed(
        provider=provider,
        client=client,
    ).parsed


async def asyncio_detailed(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingConnectionResponse | Problem]:
    """Describe the account's key for one billing provider.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingConnectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
) -> BillingConnectionResponse | Problem | None:
    """Describe the account's key for one billing provider.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingConnectionResponse | Problem
    """

    return (
        await asyncio_detailed(
            provider=provider,
            client=client,
        )
    ).parsed
