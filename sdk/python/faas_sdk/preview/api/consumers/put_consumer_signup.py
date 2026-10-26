from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.consumer_signup_settings_response import ConsumerSignupSettingsResponse
from ...models.problem import Problem
from ...models.put_consumer_signup_request import PutConsumerSignupRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PutConsumerSignupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/consumer-signup".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConsumerSignupSettingsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ConsumerSignupSettingsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConsumerSignupSettingsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutConsumerSignupRequest,
) -> Response[ConsumerSignupSettingsResponse | Problem]:
    """Let the app's customers sign up themselves and save a card with Stripe Checkout.

     Customers sign up on the hosted page (page_url) or through the public
    sign-up API: they pick one of plan_ids (none offers the default
    plan), save a card in the account's own Stripe account, and get a
    consumer key with key_scopes. Needs the account's Stripe billing
    connection. A restricted Stripe key needs write access to Customers
    and Checkout Sessions and read access to SetupIntents.

    Args:
        slug (str):
        body (PutConsumerSignupRequest): Self-service sign-up settings (ADR-20261021143000).
            Omitted fields take their defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerSignupSettingsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutConsumerSignupRequest,
) -> ConsumerSignupSettingsResponse | Problem | None:
    """Let the app's customers sign up themselves and save a card with Stripe Checkout.

     Customers sign up on the hosted page (page_url) or through the public
    sign-up API: they pick one of plan_ids (none offers the default
    plan), save a card in the account's own Stripe account, and get a
    consumer key with key_scopes. Needs the account's Stripe billing
    connection. A restricted Stripe key needs write access to Customers
    and Checkout Sessions and read access to SetupIntents.

    Args:
        slug (str):
        body (PutConsumerSignupRequest): Self-service sign-up settings (ADR-20261021143000).
            Omitted fields take their defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerSignupSettingsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutConsumerSignupRequest,
) -> Response[ConsumerSignupSettingsResponse | Problem]:
    """Let the app's customers sign up themselves and save a card with Stripe Checkout.

     Customers sign up on the hosted page (page_url) or through the public
    sign-up API: they pick one of plan_ids (none offers the default
    plan), save a card in the account's own Stripe account, and get a
    consumer key with key_scopes. Needs the account's Stripe billing
    connection. A restricted Stripe key needs write access to Customers
    and Checkout Sessions and read access to SetupIntents.

    Args:
        slug (str):
        body (PutConsumerSignupRequest): Self-service sign-up settings (ADR-20261021143000).
            Omitted fields take their defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerSignupSettingsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutConsumerSignupRequest,
) -> ConsumerSignupSettingsResponse | Problem | None:
    """Let the app's customers sign up themselves and save a card with Stripe Checkout.

     Customers sign up on the hosted page (page_url) or through the public
    sign-up API: they pick one of plan_ids (none offers the default
    plan), save a card in the account's own Stripe account, and get a
    consumer key with key_scopes. Needs the account's Stripe billing
    connection. A restricted Stripe key needs write access to Customers
    and Checkout Sessions and read access to SetupIntents.

    Args:
        slug (str):
        body (PutConsumerSignupRequest): Self-service sign-up settings (ADR-20261021143000).
            Omitted fields take their defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerSignupSettingsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
