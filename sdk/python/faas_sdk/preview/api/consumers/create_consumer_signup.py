from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_consumer_signup_request import CreateConsumerSignupRequest
from ...models.create_consumer_signup_response import CreateConsumerSignupResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateConsumerSignupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/consumer-signup/{slug}".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CreateConsumerSignupResponse | Problem | None:
    if response.status_code == 201:
        response_201 = CreateConsumerSignupResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CreateConsumerSignupResponse | Problem]:
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
    body: CreateConsumerSignupRequest,
) -> Response[CreateConsumerSignupResponse | Problem]:
    """Start a sign-up and get a Stripe Checkout URL to save a card.

     Needs no credential. Creates a Stripe customer in the app owner's
    Stripe account and a setup-mode Checkout Session; nothing is charged.
    The claim token is returned only here and completes the sign-up
    within 24 hours.

    Args:
        slug (str):
        body (CreateConsumerSignupRequest): A customer's sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreateConsumerSignupResponse | Problem]
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
    body: CreateConsumerSignupRequest,
) -> CreateConsumerSignupResponse | Problem | None:
    """Start a sign-up and get a Stripe Checkout URL to save a card.

     Needs no credential. Creates a Stripe customer in the app owner's
    Stripe account and a setup-mode Checkout Session; nothing is charged.
    The claim token is returned only here and completes the sign-up
    within 24 hours.

    Args:
        slug (str):
        body (CreateConsumerSignupRequest): A customer's sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreateConsumerSignupResponse | Problem
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
    body: CreateConsumerSignupRequest,
) -> Response[CreateConsumerSignupResponse | Problem]:
    """Start a sign-up and get a Stripe Checkout URL to save a card.

     Needs no credential. Creates a Stripe customer in the app owner's
    Stripe account and a setup-mode Checkout Session; nothing is charged.
    The claim token is returned only here and completes the sign-up
    within 24 hours.

    Args:
        slug (str):
        body (CreateConsumerSignupRequest): A customer's sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreateConsumerSignupResponse | Problem]
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
    body: CreateConsumerSignupRequest,
) -> CreateConsumerSignupResponse | Problem | None:
    """Start a sign-up and get a Stripe Checkout URL to save a card.

     Needs no credential. Creates a Stripe customer in the app owner's
    Stripe account and a setup-mode Checkout Session; nothing is charged.
    The claim token is returned only here and completes the sign-up
    within 24 hours.

    Args:
        slug (str):
        body (CreateConsumerSignupRequest): A customer's sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreateConsumerSignupResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
