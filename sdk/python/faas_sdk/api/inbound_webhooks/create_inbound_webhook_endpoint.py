from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_inbound_webhook_endpoint_request import CreateInboundWebhookEndpointRequest
from ...models.inbound_webhook_endpoint_response import InboundWebhookEndpointResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateInboundWebhookEndpointRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/inbound-webhooks".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InboundWebhookEndpointResponse | Problem | None:
    if response.status_code == 201:
        response_201 = InboundWebhookEndpointResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[InboundWebhookEndpointResponse | Problem]:
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
    body: CreateInboundWebhookEndpointRequest,
) -> Response[InboundWebhookEndpointResponse | Problem]:
    """Create a provider-verified durable webhook endpoint.

     Returns the public endpoint_url once. Gregale stores only a SHA-256
    digest of its opaque token and an age/X25519-sealed provider signing
    secret. Hobby, Pro, and Scale plans are supported.

    Args:
        slug (str):
        body (CreateInboundWebhookEndpointRequest): Create a provider-verified endpoint whose
            accepted events become durable app invocations. Example: {'name': 'stripe-primary',
            'provider': 'stripe', 'signing_secret': 'whsec_example', 'delivery_path':
            '/internal/stripe'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InboundWebhookEndpointResponse | Problem]
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
    body: CreateInboundWebhookEndpointRequest,
) -> InboundWebhookEndpointResponse | Problem | None:
    """Create a provider-verified durable webhook endpoint.

     Returns the public endpoint_url once. Gregale stores only a SHA-256
    digest of its opaque token and an age/X25519-sealed provider signing
    secret. Hobby, Pro, and Scale plans are supported.

    Args:
        slug (str):
        body (CreateInboundWebhookEndpointRequest): Create a provider-verified endpoint whose
            accepted events become durable app invocations. Example: {'name': 'stripe-primary',
            'provider': 'stripe', 'signing_secret': 'whsec_example', 'delivery_path':
            '/internal/stripe'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InboundWebhookEndpointResponse | Problem
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
    body: CreateInboundWebhookEndpointRequest,
) -> Response[InboundWebhookEndpointResponse | Problem]:
    """Create a provider-verified durable webhook endpoint.

     Returns the public endpoint_url once. Gregale stores only a SHA-256
    digest of its opaque token and an age/X25519-sealed provider signing
    secret. Hobby, Pro, and Scale plans are supported.

    Args:
        slug (str):
        body (CreateInboundWebhookEndpointRequest): Create a provider-verified endpoint whose
            accepted events become durable app invocations. Example: {'name': 'stripe-primary',
            'provider': 'stripe', 'signing_secret': 'whsec_example', 'delivery_path':
            '/internal/stripe'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InboundWebhookEndpointResponse | Problem]
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
    body: CreateInboundWebhookEndpointRequest,
) -> InboundWebhookEndpointResponse | Problem | None:
    """Create a provider-verified durable webhook endpoint.

     Returns the public endpoint_url once. Gregale stores only a SHA-256
    digest of its opaque token and an age/X25519-sealed provider signing
    secret. Hobby, Pro, and Scale plans are supported.

    Args:
        slug (str):
        body (CreateInboundWebhookEndpointRequest): Create a provider-verified endpoint whose
            accepted events become durable app invocations. Example: {'name': 'stripe-primary',
            'provider': 'stripe', 'signing_secret': 'whsec_example', 'delivery_path':
            '/internal/stripe'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InboundWebhookEndpointResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
