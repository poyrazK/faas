from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_webhook_response import PlatformTenantWebhookResponse
from ...models.problem import Problem
from ...models.update_platform_tenant_webhook_request import UpdatePlatformTenantWebhookRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    webhook_id: UUID,
    *,
    body: UpdatePlatformTenantWebhookRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account/platform-tenants/{id}/webhooks/{webhook_id}".format(
            id=quote(str(id), safe=""),
            webhook_id=quote(str(webhook_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantWebhookResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantWebhookResponse.from_dict(response.json())

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
) -> Response[PlatformTenantWebhookResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    webhook_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdatePlatformTenantWebhookRequest,
) -> Response[PlatformTenantWebhookResponse | Problem]:
    """Update a tenant webhook destination or delivery policy.

    Args:
        id (UUID):
        webhook_id (UUID):
        body (UpdatePlatformTenantWebhookRequest): Update a tenant statement receiver. Rotate
            secrets with the dedicated action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantWebhookResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        webhook_id=webhook_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    webhook_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdatePlatformTenantWebhookRequest,
) -> PlatformTenantWebhookResponse | Problem | None:
    """Update a tenant webhook destination or delivery policy.

    Args:
        id (UUID):
        webhook_id (UUID):
        body (UpdatePlatformTenantWebhookRequest): Update a tenant statement receiver. Rotate
            secrets with the dedicated action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantWebhookResponse | Problem
    """

    return sync_detailed(
        id=id,
        webhook_id=webhook_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    webhook_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdatePlatformTenantWebhookRequest,
) -> Response[PlatformTenantWebhookResponse | Problem]:
    """Update a tenant webhook destination or delivery policy.

    Args:
        id (UUID):
        webhook_id (UUID):
        body (UpdatePlatformTenantWebhookRequest): Update a tenant statement receiver. Rotate
            secrets with the dedicated action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantWebhookResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        webhook_id=webhook_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    webhook_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdatePlatformTenantWebhookRequest,
) -> PlatformTenantWebhookResponse | Problem | None:
    """Update a tenant webhook destination or delivery policy.

    Args:
        id (UUID):
        webhook_id (UUID):
        body (UpdatePlatformTenantWebhookRequest): Update a tenant statement receiver. Rotate
            secrets with the dedicated action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantWebhookResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            webhook_id=webhook_id,
            client=client,
            body=body,
        )
    ).parsed
