from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_connection_response import BillingConnectionResponse
from ...models.billing_provider import BillingProvider
from ...models.problem import Problem
from ...models.put_billing_connection_request import PutBillingConnectionRequest
from ...types import Response


def _get_kwargs(
    provider: BillingProvider,
    *,
    body: PutBillingConnectionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/billing-connections/{provider}".format(
            provider=quote(str(provider), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

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
    body: PutBillingConnectionRequest,
) -> Response[BillingConnectionResponse | Problem]:
    """Store or replace the account's key for one billing provider.

     Checks the key against the provider, then seals it at rest. Stripe
    takes a restricted key (rk_live_… or rk_test_…) with read and write
    access to invoices and invoice items; Paddle an API key
    (pdl_live_apikey_… or pdl_sdbx_apikey_…) with transaction access and
    read access to customers; Lago
    an API key and, for the EU region or a self-hosted Lago, api_url. Only
    the last four characters, the mode and the Lago API URL are ever
    returned.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).
        body (PutBillingConnectionRequest): The account's own key for one billing provider
            (ADR-1502, ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingConnectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
    body: PutBillingConnectionRequest,
) -> BillingConnectionResponse | Problem | None:
    """Store or replace the account's key for one billing provider.

     Checks the key against the provider, then seals it at rest. Stripe
    takes a restricted key (rk_live_… or rk_test_…) with read and write
    access to invoices and invoice items; Paddle an API key
    (pdl_live_apikey_… or pdl_sdbx_apikey_…) with transaction access and
    read access to customers; Lago
    an API key and, for the EU region or a self-hosted Lago, api_url. Only
    the last four characters, the mode and the Lago API URL are ever
    returned.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).
        body (PutBillingConnectionRequest): The account's own key for one billing provider
            (ADR-1502, ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingConnectionResponse | Problem
    """

    return sync_detailed(
        provider=provider,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
    body: PutBillingConnectionRequest,
) -> Response[BillingConnectionResponse | Problem]:
    """Store or replace the account's key for one billing provider.

     Checks the key against the provider, then seals it at rest. Stripe
    takes a restricted key (rk_live_… or rk_test_…) with read and write
    access to invoices and invoice items; Paddle an API key
    (pdl_live_apikey_… or pdl_sdbx_apikey_…) with transaction access and
    read access to customers; Lago
    an API key and, for the EU region or a self-hosted Lago, api_url. Only
    the last four characters, the mode and the Lago API URL are ever
    returned.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).
        body (PutBillingConnectionRequest): The account's own key for one billing provider
            (ADR-1502, ADR-1507).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingConnectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    provider: BillingProvider,
    *,
    client: AuthenticatedClient | Client,
    body: PutBillingConnectionRequest,
) -> BillingConnectionResponse | Problem | None:
    """Store or replace the account's key for one billing provider.

     Checks the key against the provider, then seals it at rest. Stripe
    takes a restricted key (rk_live_… or rk_test_…) with read and write
    access to invoices and invoice items; Paddle an API key
    (pdl_live_apikey_… or pdl_sdbx_apikey_…) with transaction access and
    read access to customers; Lago
    an API key and, for the EU region or a self-hosted Lago, api_url. Only
    the last four characters, the mode and the Lago API URL are ever
    returned.

    Args:
        provider (BillingProvider): A billing provider statements export to (ADR-1507).
        body (PutBillingConnectionRequest): The account's own key for one billing provider
            (ADR-1502, ADR-1507).

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
            body=body,
        )
    ).parsed
