from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_attention_action import MCPReadinessAttentionAction
from ...models.mcp_readiness_attention_receipt import MCPReadinessAttentionReceipt
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: MCPReadinessAttentionAction,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mcp-readiness-incidents/{id}/attention-actions".format(
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
) -> Any | MCPReadinessAttentionReceipt | None:
    if response.status_code == 200:
        response_200 = MCPReadinessAttentionReceipt.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessAttentionReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessAttentionAction,
) -> Response[Any | MCPReadinessAttentionReceipt]:
    """Acknowledge an incident or configure finite reminders

     Admin scope and MFA required. New decisions require an open readiness webhook incident and its
    current attention version. Disabled rules may be configured but cannot create or deliver reminders.
    Identical app-scoped request UUID retries return their durable receipt even after expiry or
    recovery; changed requests conflict. Acknowledgement only prevents reminder creation and suppresses
    unleased reminders. In-flight delivery may finish. Initial failure and recovery notifications retain
    their behavior. Reminders inherit optional rule defaults at incident opening and may be overridden
    per incident. Their lifetime creation budget never resets. Configure zero interval and zero budget
    to disable. Absolute acknowledgement expiry must be in the future and no more than 30 days away;
    omission acknowledges indefinitely. Native paging and fallback destinations are not configured here.

    Args:
        slug (str):
        id (UUID):
        body (MCPReadinessAttentionAction): Version-fenced attention decision. Acknowledge accepts
            optional expires_at; configure_reminders requires reminder_policy; release accepts
            neither.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessAttentionReceipt]
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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessAttentionAction,
) -> Any | MCPReadinessAttentionReceipt | None:
    """Acknowledge an incident or configure finite reminders

     Admin scope and MFA required. New decisions require an open readiness webhook incident and its
    current attention version. Disabled rules may be configured but cannot create or deliver reminders.
    Identical app-scoped request UUID retries return their durable receipt even after expiry or
    recovery; changed requests conflict. Acknowledgement only prevents reminder creation and suppresses
    unleased reminders. In-flight delivery may finish. Initial failure and recovery notifications retain
    their behavior. Reminders inherit optional rule defaults at incident opening and may be overridden
    per incident. Their lifetime creation budget never resets. Configure zero interval and zero budget
    to disable. Absolute acknowledgement expiry must be in the future and no more than 30 days away;
    omission acknowledges indefinitely. Native paging and fallback destinations are not configured here.

    Args:
        slug (str):
        id (UUID):
        body (MCPReadinessAttentionAction): Version-fenced attention decision. Acknowledge accepts
            optional expires_at; configure_reminders requires reminder_policy; release accepts
            neither.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessAttentionReceipt
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessAttentionAction,
) -> Response[Any | MCPReadinessAttentionReceipt]:
    """Acknowledge an incident or configure finite reminders

     Admin scope and MFA required. New decisions require an open readiness webhook incident and its
    current attention version. Disabled rules may be configured but cannot create or deliver reminders.
    Identical app-scoped request UUID retries return their durable receipt even after expiry or
    recovery; changed requests conflict. Acknowledgement only prevents reminder creation and suppresses
    unleased reminders. In-flight delivery may finish. Initial failure and recovery notifications retain
    their behavior. Reminders inherit optional rule defaults at incident opening and may be overridden
    per incident. Their lifetime creation budget never resets. Configure zero interval and zero budget
    to disable. Absolute acknowledgement expiry must be in the future and no more than 30 days away;
    omission acknowledges indefinitely. Native paging and fallback destinations are not configured here.

    Args:
        slug (str):
        id (UUID):
        body (MCPReadinessAttentionAction): Version-fenced attention decision. Acknowledge accepts
            optional expires_at; configure_reminders requires reminder_policy; release accepts
            neither.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessAttentionReceipt]
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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPReadinessAttentionAction,
) -> Any | MCPReadinessAttentionReceipt | None:
    """Acknowledge an incident or configure finite reminders

     Admin scope and MFA required. New decisions require an open readiness webhook incident and its
    current attention version. Disabled rules may be configured but cannot create or deliver reminders.
    Identical app-scoped request UUID retries return their durable receipt even after expiry or
    recovery; changed requests conflict. Acknowledgement only prevents reminder creation and suppresses
    unleased reminders. In-flight delivery may finish. Initial failure and recovery notifications retain
    their behavior. Reminders inherit optional rule defaults at incident opening and may be overridden
    per incident. Their lifetime creation budget never resets. Configure zero interval and zero budget
    to disable. Absolute acknowledgement expiry must be in the future and no more than 30 days away;
    omission acknowledges indefinitely. Native paging and fallback destinations are not configured here.

    Args:
        slug (str):
        id (UUID):
        body (MCPReadinessAttentionAction): Version-fenced attention decision. Acknowledge accepts
            optional expires_at; configure_reminders requires reminder_policy; release accepts
            neither.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessAttentionReceipt
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
