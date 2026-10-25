from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_cancel_response import BillingCancelResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/billing/cancel",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingCancelResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingCancelResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingCancelResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingCancelResponse | Problem]:
    r"""Set cancel_at_period_end on the active subscription; keep the account active until period end.

     Stripe: `Subscriptions.Update(cancel_at_period_end=true)`.
    Paddle: `Customer.Update(scheduled_change=cancel)` on the
    customer's stored object.

    Returns the effective cancel timestamp (`current_period_end`
    on Stripe; the next month-rollover instant on Paddle) in
    RFC 3339 so the CLI can print \"your apps will stop on …\".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingCancelResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> BillingCancelResponse | Problem | None:
    r"""Set cancel_at_period_end on the active subscription; keep the account active until period end.

     Stripe: `Subscriptions.Update(cancel_at_period_end=true)`.
    Paddle: `Customer.Update(scheduled_change=cancel)` on the
    customer's stored object.

    Returns the effective cancel timestamp (`current_period_end`
    on Stripe; the next month-rollover instant on Paddle) in
    RFC 3339 so the CLI can print \"your apps will stop on …\".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingCancelResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingCancelResponse | Problem]:
    r"""Set cancel_at_period_end on the active subscription; keep the account active until period end.

     Stripe: `Subscriptions.Update(cancel_at_period_end=true)`.
    Paddle: `Customer.Update(scheduled_change=cancel)` on the
    customer's stored object.

    Returns the effective cancel timestamp (`current_period_end`
    on Stripe; the next month-rollover instant on Paddle) in
    RFC 3339 so the CLI can print \"your apps will stop on …\".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingCancelResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> BillingCancelResponse | Problem | None:
    r"""Set cancel_at_period_end on the active subscription; keep the account active until period end.

     Stripe: `Subscriptions.Update(cancel_at_period_end=true)`.
    Paddle: `Customer.Update(scheduled_change=cancel)` on the
    customer's stored object.

    Returns the effective cancel timestamp (`current_period_end`
    on Stripe; the next month-rollover instant on Paddle) in
    RFC 3339 so the CLI can print \"your apps will stop on …\".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingCancelResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
