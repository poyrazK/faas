from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.link_billing_run_stripe_customer_request import LinkBillingRunStripeCustomerRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    consumer_id: UUID,
    *,
    body: LinkBillingRunStripeCustomerRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/billing-runs/{id}/draft-exports/{consumer_id}/stripe-customer".format(
            id=quote(str(id), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: LinkBillingRunStripeCustomerRequest,
) -> Response[Any | Problem]:
    """Link a Stripe customer from a blocked draft review row.

     Verifies a customer in the reviewed Stripe account and mode and saves it for future draft reviews.

    Args:
        id (UUID):
        consumer_id (UUID):
        body (LinkBillingRunStripeCustomerRequest): The Stripe customer and original draft review
            being linked.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        consumer_id=consumer_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: LinkBillingRunStripeCustomerRequest,
) -> Any | Problem | None:
    """Link a Stripe customer from a blocked draft review row.

     Verifies a customer in the reviewed Stripe account and mode and saves it for future draft reviews.

    Args:
        id (UUID):
        consumer_id (UUID):
        body (LinkBillingRunStripeCustomerRequest): The Stripe customer and original draft review
            being linked.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        id=id,
        consumer_id=consumer_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: LinkBillingRunStripeCustomerRequest,
) -> Response[Any | Problem]:
    """Link a Stripe customer from a blocked draft review row.

     Verifies a customer in the reviewed Stripe account and mode and saves it for future draft reviews.

    Args:
        id (UUID):
        consumer_id (UUID):
        body (LinkBillingRunStripeCustomerRequest): The Stripe customer and original draft review
            being linked.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        consumer_id=consumer_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: LinkBillingRunStripeCustomerRequest,
) -> Any | Problem | None:
    """Link a Stripe customer from a blocked draft review row.

     Verifies a customer in the reviewed Stripe account and mode and saves it for future draft reviews.

    Args:
        id (UUID):
        consumer_id (UUID):
        body (LinkBillingRunStripeCustomerRequest): The Stripe customer and original draft review
            being linked.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            consumer_id=consumer_id,
            client=client,
            body=body,
        )
    ).parsed
