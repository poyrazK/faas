from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_connection_response import BillingConnectionResponse
from ...models.problem import Problem
from ...models.put_stripe_connection_request import PutStripeConnectionRequest
from ...types import Response


def _get_kwargs(
    *,
    body: PutStripeConnectionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/billing-connections/stripe",
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
    *,
    client: AuthenticatedClient | Client,
    body: PutStripeConnectionRequest,
) -> Response[BillingConnectionResponse | Problem]:
    """Store or replace the account's Stripe key.

     Checks the key against Stripe, then seals it at rest. Use a restricted
    key (rk_live_… or rk_test_…) with read and write access to invoices
    and invoice items. Only the last four characters and the mode are
    ever returned.

    Args:
        body (PutStripeConnectionRequest): The account's own Stripe key for statement invoice
            exports (ADR-976).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingConnectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: PutStripeConnectionRequest,
) -> BillingConnectionResponse | Problem | None:
    """Store or replace the account's Stripe key.

     Checks the key against Stripe, then seals it at rest. Use a restricted
    key (rk_live_… or rk_test_…) with read and write access to invoices
    and invoice items. Only the last four characters and the mode are
    ever returned.

    Args:
        body (PutStripeConnectionRequest): The account's own Stripe key for statement invoice
            exports (ADR-976).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingConnectionResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PutStripeConnectionRequest,
) -> Response[BillingConnectionResponse | Problem]:
    """Store or replace the account's Stripe key.

     Checks the key against Stripe, then seals it at rest. Use a restricted
    key (rk_live_… or rk_test_…) with read and write access to invoices
    and invoice items. Only the last four characters and the mode are
    ever returned.

    Args:
        body (PutStripeConnectionRequest): The account's own Stripe key for statement invoice
            exports (ADR-976).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingConnectionResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PutStripeConnectionRequest,
) -> BillingConnectionResponse | Problem | None:
    """Store or replace the account's Stripe key.

     Checks the key against Stripe, then seals it at rest. Use a restricted
    key (rk_live_… or rk_test_…) with read and write access to invoices
    and invoice items. Only the last four characters and the mode are
    ever returned.

    Args:
        body (PutStripeConnectionRequest): The account's own Stripe key for statement invoice
            exports (ADR-976).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingConnectionResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
