from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_webhook_retry_delivery_response import AppWebhookRetryDeliveryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    webhook_id: UUID,
    did: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/webhooks/{webhook_id}/deliveries/{did}/retry".format(
            id=quote(str(id), safe=""),
            webhook_id=quote(str(webhook_id), safe=""),
            did=quote(str(did), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppWebhookRetryDeliveryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppWebhookRetryDeliveryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppWebhookRetryDeliveryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    webhook_id: UUID,
    did: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppWebhookRetryDeliveryResponse | Problem]:
    """Retry one dead tenant webhook delivery.

    Args:
        id (UUID):
        webhook_id (UUID):
        did (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookRetryDeliveryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        webhook_id=webhook_id,
        did=did,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    webhook_id: UUID,
    did: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AppWebhookRetryDeliveryResponse | Problem | None:
    """Retry one dead tenant webhook delivery.

    Args:
        id (UUID):
        webhook_id (UUID):
        did (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookRetryDeliveryResponse | Problem
    """

    return sync_detailed(
        id=id,
        webhook_id=webhook_id,
        did=did,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    webhook_id: UUID,
    did: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppWebhookRetryDeliveryResponse | Problem]:
    """Retry one dead tenant webhook delivery.

    Args:
        id (UUID):
        webhook_id (UUID):
        did (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookRetryDeliveryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        webhook_id=webhook_id,
        did=did,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    webhook_id: UUID,
    did: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AppWebhookRetryDeliveryResponse | Problem | None:
    """Retry one dead tenant webhook delivery.

    Args:
        id (UUID):
        webhook_id (UUID):
        did (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookRetryDeliveryResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            webhook_id=webhook_id,
            did=did,
            client=client,
        )
    ).parsed
