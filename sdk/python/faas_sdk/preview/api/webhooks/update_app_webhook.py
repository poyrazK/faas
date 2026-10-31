from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_webhook_response import AppWebhookResponse
from ...models.problem import Problem
from ...models.update_app_webhook_request import UpdateAppWebhookRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: UpdateAppWebhookRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/webhooks/{id}".format(
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
) -> AppWebhookResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppWebhookResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[AppWebhookResponse | Problem]:
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
    body: UpdateAppWebhookRequest,
) -> Response[AppWebhookResponse | Problem]:
    """Partial-update a webhook subscription.

     Every field is optional. To rotate the secret in place, send
    `webhook_secret` (the handler seals it; the response carries
    the masked constant). To rotate via the dedicated endpoint,
    use POST /webhooks/{id}/rotate-secret.

    Args:
        slug (str):
        id (str):
        body (UpdateAppWebhookRequest): Partial update of an existing webhook subscription. Every
            field is optional — the handler merges the supplied fields
            onto the current row. omit a field to leave it unchanged.
             Example: {'target_url': 'https://example.com/hook2', 'delivery_format': 'cloudevents',
            'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookResponse | Problem]
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
    body: UpdateAppWebhookRequest,
) -> AppWebhookResponse | Problem | None:
    """Partial-update a webhook subscription.

     Every field is optional. To rotate the secret in place, send
    `webhook_secret` (the handler seals it; the response carries
    the masked constant). To rotate via the dedicated endpoint,
    use POST /webhooks/{id}/rotate-secret.

    Args:
        slug (str):
        id (str):
        body (UpdateAppWebhookRequest): Partial update of an existing webhook subscription. Every
            field is optional — the handler merges the supplied fields
            onto the current row. omit a field to leave it unchanged.
             Example: {'target_url': 'https://example.com/hook2', 'delivery_format': 'cloudevents',
            'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookResponse | Problem
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
    body: UpdateAppWebhookRequest,
) -> Response[AppWebhookResponse | Problem]:
    """Partial-update a webhook subscription.

     Every field is optional. To rotate the secret in place, send
    `webhook_secret` (the handler seals it; the response carries
    the masked constant). To rotate via the dedicated endpoint,
    use POST /webhooks/{id}/rotate-secret.

    Args:
        slug (str):
        id (str):
        body (UpdateAppWebhookRequest): Partial update of an existing webhook subscription. Every
            field is optional — the handler merges the supplied fields
            onto the current row. omit a field to leave it unchanged.
             Example: {'target_url': 'https://example.com/hook2', 'delivery_format': 'cloudevents',
            'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookResponse | Problem]
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
    body: UpdateAppWebhookRequest,
) -> AppWebhookResponse | Problem | None:
    """Partial-update a webhook subscription.

     Every field is optional. To rotate the secret in place, send
    `webhook_secret` (the handler seals it; the response carries
    the masked constant). To rotate via the dedicated endpoint,
    use POST /webhooks/{id}/rotate-secret.

    Args:
        slug (str):
        id (str):
        body (UpdateAppWebhookRequest): Partial update of an existing webhook subscription. Every
            field is optional — the handler merges the supplied fields
            onto the current row. omit a field to leave it unchanged.
             Example: {'target_url': 'https://example.com/hook2', 'delivery_format': 'cloudevents',
            'enabled': True}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
