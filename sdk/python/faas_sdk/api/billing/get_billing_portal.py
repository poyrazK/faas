from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_portal_response import BillingPortalResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/billing/portal",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingPortalResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingPortalResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingPortalResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingPortalResponse | Problem]:
    """Get a provider billing portal URL (and the card-on-file summary).

     Returns the URL the customer should be sent to in order to
    manage their subscription (update card, view invoices,
    download receipts, cancel). When the active provider exposes
    customer sessions, the server creates a short-lived authenticated
    portal URL. Otherwise it renders the operator's
    `FAAS_BILLING_PORTAL_URL` template.

    The response also carries a `payment_method` block (issue
    #242) — the card-on-file summary (brand, last-4, expiry).
    The CLI's `faas billing payment-method` renders from this
    field; the dashboard's billing page does the same. The
    field is omitempty so no-card-on-file responses stay clean.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingPortalResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> BillingPortalResponse | Problem | None:
    """Get a provider billing portal URL (and the card-on-file summary).

     Returns the URL the customer should be sent to in order to
    manage their subscription (update card, view invoices,
    download receipts, cancel). When the active provider exposes
    customer sessions, the server creates a short-lived authenticated
    portal URL. Otherwise it renders the operator's
    `FAAS_BILLING_PORTAL_URL` template.

    The response also carries a `payment_method` block (issue
    #242) — the card-on-file summary (brand, last-4, expiry).
    The CLI's `faas billing payment-method` renders from this
    field; the dashboard's billing page does the same. The
    field is omitempty so no-card-on-file responses stay clean.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingPortalResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingPortalResponse | Problem]:
    """Get a provider billing portal URL (and the card-on-file summary).

     Returns the URL the customer should be sent to in order to
    manage their subscription (update card, view invoices,
    download receipts, cancel). When the active provider exposes
    customer sessions, the server creates a short-lived authenticated
    portal URL. Otherwise it renders the operator's
    `FAAS_BILLING_PORTAL_URL` template.

    The response also carries a `payment_method` block (issue
    #242) — the card-on-file summary (brand, last-4, expiry).
    The CLI's `faas billing payment-method` renders from this
    field; the dashboard's billing page does the same. The
    field is omitempty so no-card-on-file responses stay clean.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingPortalResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> BillingPortalResponse | Problem | None:
    """Get a provider billing portal URL (and the card-on-file summary).

     Returns the URL the customer should be sent to in order to
    manage their subscription (update card, view invoices,
    download receipts, cancel). When the active provider exposes
    customer sessions, the server creates a short-lived authenticated
    portal URL. Otherwise it renders the operator's
    `FAAS_BILLING_PORTAL_URL` template.

    The response also carries a `payment_method` block (issue
    #242) — the card-on-file summary (brand, last-4, expiry).
    The CLI's `faas billing payment-method` renders from this
    field; the dashboard's billing page does the same. The
    field is omitempty so no-card-on-file responses stay clean.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingPortalResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
