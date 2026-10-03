from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_release_webhook_response import AccountReleaseWebhookResponse
from ...models.create_account_release_webhook_request import CreateAccountReleaseWebhookRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateAccountReleaseWebhookRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/release-webhooks",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountReleaseWebhookResponse | Problem | None:
    if response.status_code == 201:
        response_201 = AccountReleaseWebhookResponse.from_dict(response.json())

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
) -> Response[AccountReleaseWebhookResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateAccountReleaseWebhookRequest,
) -> Response[AccountReleaseWebhookResponse | Problem]:
    """Subscribe one receiver to release events from all current and future account apps.

     The target is SSRF-guarded and the signing secret is sealed at rest; the response never exposes
    plaintext.

    Args:
        body (CreateAccountReleaseWebhookRequest): Subscribe a target to release events emitted by
            every app owned by the active account. Example: {'target_url':
            'https://example.com/releases', 'webhook_secret': 'replace-with-random-secret',
            'event_filter': ['deployment.live', 'deployment.failed'], 'retry_policy': 'default',
            'delivery_format': 'cloudevents', 'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountReleaseWebhookResponse | Problem]
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
    body: CreateAccountReleaseWebhookRequest,
) -> AccountReleaseWebhookResponse | Problem | None:
    """Subscribe one receiver to release events from all current and future account apps.

     The target is SSRF-guarded and the signing secret is sealed at rest; the response never exposes
    plaintext.

    Args:
        body (CreateAccountReleaseWebhookRequest): Subscribe a target to release events emitted by
            every app owned by the active account. Example: {'target_url':
            'https://example.com/releases', 'webhook_secret': 'replace-with-random-secret',
            'event_filter': ['deployment.live', 'deployment.failed'], 'retry_policy': 'default',
            'delivery_format': 'cloudevents', 'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountReleaseWebhookResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateAccountReleaseWebhookRequest,
) -> Response[AccountReleaseWebhookResponse | Problem]:
    """Subscribe one receiver to release events from all current and future account apps.

     The target is SSRF-guarded and the signing secret is sealed at rest; the response never exposes
    plaintext.

    Args:
        body (CreateAccountReleaseWebhookRequest): Subscribe a target to release events emitted by
            every app owned by the active account. Example: {'target_url':
            'https://example.com/releases', 'webhook_secret': 'replace-with-random-secret',
            'event_filter': ['deployment.live', 'deployment.failed'], 'retry_policy': 'default',
            'delivery_format': 'cloudevents', 'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountReleaseWebhookResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateAccountReleaseWebhookRequest,
) -> AccountReleaseWebhookResponse | Problem | None:
    """Subscribe one receiver to release events from all current and future account apps.

     The target is SSRF-guarded and the signing secret is sealed at rest; the response never exposes
    plaintext.

    Args:
        body (CreateAccountReleaseWebhookRequest): Subscribe a target to release events emitted by
            every app owned by the active account. Example: {'target_url':
            'https://example.com/releases', 'webhook_secret': 'replace-with-random-secret',
            'event_filter': ['deployment.live', 'deployment.failed'], 'retry_policy': 'default',
            'delivery_format': 'cloudevents', 'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountReleaseWebhookResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
