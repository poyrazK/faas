from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_catalog_response import BillingCatalogResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/billing-paddle-catalog",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingCatalogResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingCatalogResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingCatalogResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingCatalogResponse | Problem]:
    """Read the cached Paddle price + product catalog (admin-only).

     Returns the in-memory catalog snapshot from *paddle.Provider.
    synced_at is the timestamp of the most recent successful
    EnsurePlanProducts call; an empty string means no hydration
    has run yet. On a Stripe deployment the handler returns 501
    with code billing_op_unsupported — the type assertion at
    the dispatcher fails and the surface is provider-scoped.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingCatalogResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> BillingCatalogResponse | Problem | None:
    """Read the cached Paddle price + product catalog (admin-only).

     Returns the in-memory catalog snapshot from *paddle.Provider.
    synced_at is the timestamp of the most recent successful
    EnsurePlanProducts call; an empty string means no hydration
    has run yet. On a Stripe deployment the handler returns 501
    with code billing_op_unsupported — the type assertion at
    the dispatcher fails and the surface is provider-scoped.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingCatalogResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingCatalogResponse | Problem]:
    """Read the cached Paddle price + product catalog (admin-only).

     Returns the in-memory catalog snapshot from *paddle.Provider.
    synced_at is the timestamp of the most recent successful
    EnsurePlanProducts call; an empty string means no hydration
    has run yet. On a Stripe deployment the handler returns 501
    with code billing_op_unsupported — the type assertion at
    the dispatcher fails and the surface is provider-scoped.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingCatalogResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> BillingCatalogResponse | Problem | None:
    """Read the cached Paddle price + product catalog (admin-only).

     Returns the in-memory catalog snapshot from *paddle.Provider.
    synced_at is the timestamp of the most recent successful
    EnsurePlanProducts call; an empty string means no hydration
    has run yet. On a Stripe deployment the handler returns 501
    with code billing_op_unsupported — the type assertion at
    the dispatcher fails and the surface is provider-scoped.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingCatalogResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
