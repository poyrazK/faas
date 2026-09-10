from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_webhook_response import AppWebhookResponse
from ...models.create_app_webhook_request import CreateAppWebhookRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateAppWebhookRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/webhooks".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppWebhookResponse | Problem | None:
    if response.status_code == 201:
        response_201 = AppWebhookResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppWebhookResponse | Problem]:
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
    body: CreateAppWebhookRequest,
) -> Response[AppWebhookResponse | Problem]:
    """Create a new outbound webhook subscription.

     target_url is SSRF-guarded at write time (loopback / RFC1918
    / link-local / metadata IPs are rejected unless
    FAAS_EGRESS_ALLOW_LOOPBACK=1). webhook_secret arrives in
    the body, is sealed with secretbox.SealBytes under the
    APP_WEBHOOK namespace, and is NEVER returned in plaintext
    — the response shape carries the masked constant. event_filter
    is an optional allowlist; empty subscribes to every event in
    the closed vocabulary.

    Args:
        slug (str):
        body (CreateAppWebhookRequest): Subscribe a target URL to events emitted by the app. The
            webhook_secret is HMAC-SHA256 sealed at rest with the host
            X25519 recipient (namespace `APP_WEBHOOK`).
             Example: {'target_url': 'https://example.com/hook', 'webhook_secret': 'shh',
            'event_filter': ['cron.fired', 'app.created'], 'retry_policy': 'default', 'enabled':
            True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookResponse | Problem]
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
    body: CreateAppWebhookRequest,
) -> AppWebhookResponse | Problem | None:
    """Create a new outbound webhook subscription.

     target_url is SSRF-guarded at write time (loopback / RFC1918
    / link-local / metadata IPs are rejected unless
    FAAS_EGRESS_ALLOW_LOOPBACK=1). webhook_secret arrives in
    the body, is sealed with secretbox.SealBytes under the
    APP_WEBHOOK namespace, and is NEVER returned in plaintext
    — the response shape carries the masked constant. event_filter
    is an optional allowlist; empty subscribes to every event in
    the closed vocabulary.

    Args:
        slug (str):
        body (CreateAppWebhookRequest): Subscribe a target URL to events emitted by the app. The
            webhook_secret is HMAC-SHA256 sealed at rest with the host
            X25519 recipient (namespace `APP_WEBHOOK`).
             Example: {'target_url': 'https://example.com/hook', 'webhook_secret': 'shh',
            'event_filter': ['cron.fired', 'app.created'], 'retry_policy': 'default', 'enabled':
            True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookResponse | Problem
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
    body: CreateAppWebhookRequest,
) -> Response[AppWebhookResponse | Problem]:
    """Create a new outbound webhook subscription.

     target_url is SSRF-guarded at write time (loopback / RFC1918
    / link-local / metadata IPs are rejected unless
    FAAS_EGRESS_ALLOW_LOOPBACK=1). webhook_secret arrives in
    the body, is sealed with secretbox.SealBytes under the
    APP_WEBHOOK namespace, and is NEVER returned in plaintext
    — the response shape carries the masked constant. event_filter
    is an optional allowlist; empty subscribes to every event in
    the closed vocabulary.

    Args:
        slug (str):
        body (CreateAppWebhookRequest): Subscribe a target URL to events emitted by the app. The
            webhook_secret is HMAC-SHA256 sealed at rest with the host
            X25519 recipient (namespace `APP_WEBHOOK`).
             Example: {'target_url': 'https://example.com/hook', 'webhook_secret': 'shh',
            'event_filter': ['cron.fired', 'app.created'], 'retry_policy': 'default', 'enabled':
            True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookResponse | Problem]
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
    body: CreateAppWebhookRequest,
) -> AppWebhookResponse | Problem | None:
    """Create a new outbound webhook subscription.

     target_url is SSRF-guarded at write time (loopback / RFC1918
    / link-local / metadata IPs are rejected unless
    FAAS_EGRESS_ALLOW_LOOPBACK=1). webhook_secret arrives in
    the body, is sealed with secretbox.SealBytes under the
    APP_WEBHOOK namespace, and is NEVER returned in plaintext
    — the response shape carries the masked constant. event_filter
    is an optional allowlist; empty subscribes to every event in
    the closed vocabulary.

    Args:
        slug (str):
        body (CreateAppWebhookRequest): Subscribe a target URL to events emitted by the app. The
            webhook_secret is HMAC-SHA256 sealed at rest with the host
            X25519 recipient (namespace `APP_WEBHOOK`).
             Example: {'target_url': 'https://example.com/hook', 'webhook_secret': 'shh',
            'event_filter': ['cron.fired', 'app.created'], 'retry_policy': 'default', 'enabled':
            True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
