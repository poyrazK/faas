from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.rotate_app_webhook_secret_request import RotateAppWebhookSecretRequest
from ...models.rotate_app_webhook_secret_response import RotateAppWebhookSecretResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: RotateAppWebhookSecretRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/webhooks/{id}/rotate-secret".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RotateAppWebhookSecretResponse | None:
    if response.status_code == 200:
        response_200 = RotateAppWebhookSecretResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RotateAppWebhookSecretResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateAppWebhookSecretRequest,
) -> Response[Problem | RotateAppWebhookSecretResponse]:
    """Replace a webhook HMAC secret.

     Seals the caller-supplied replacement and overwrites the row's
    ciphertext in place. Supply the same value to the receiver. The
    plaintext is never returned; the response carries the masked
    constant and rotated_at only.

    Args:
        slug (str):
        id (str):
        body (RotateAppWebhookSecretRequest): Caller-supplied replacement signing secret. The
            value is sealed at
            rest and never returned in a response.
             Example: {'webhook_secret': 'replacement-secret-from-manager'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateAppWebhookSecretResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateAppWebhookSecretRequest,
) -> Problem | RotateAppWebhookSecretResponse | None:
    """Replace a webhook HMAC secret.

     Seals the caller-supplied replacement and overwrites the row's
    ciphertext in place. Supply the same value to the receiver. The
    plaintext is never returned; the response carries the masked
    constant and rotated_at only.

    Args:
        slug (str):
        id (str):
        body (RotateAppWebhookSecretRequest): Caller-supplied replacement signing secret. The
            value is sealed at
            rest and never returned in a response.
             Example: {'webhook_secret': 'replacement-secret-from-manager'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateAppWebhookSecretResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateAppWebhookSecretRequest,
) -> Response[Problem | RotateAppWebhookSecretResponse]:
    """Replace a webhook HMAC secret.

     Seals the caller-supplied replacement and overwrites the row's
    ciphertext in place. Supply the same value to the receiver. The
    plaintext is never returned; the response carries the masked
    constant and rotated_at only.

    Args:
        slug (str):
        id (str):
        body (RotateAppWebhookSecretRequest): Caller-supplied replacement signing secret. The
            value is sealed at
            rest and never returned in a response.
             Example: {'webhook_secret': 'replacement-secret-from-manager'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateAppWebhookSecretResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateAppWebhookSecretRequest,
) -> Problem | RotateAppWebhookSecretResponse | None:
    """Replace a webhook HMAC secret.

     Seals the caller-supplied replacement and overwrites the row's
    ciphertext in place. Supply the same value to the receiver. The
    plaintext is never returned; the response carries the masked
    constant and rotated_at only.

    Args:
        slug (str):
        id (str):
        body (RotateAppWebhookSecretRequest): Caller-supplied replacement signing secret. The
            value is sealed at
            rest and never returned in a response.
             Example: {'webhook_secret': 'replacement-secret-from-manager'}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateAppWebhookSecretResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
