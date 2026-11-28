from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_notification_action import MCPReadinessNotificationAction
from ...models.mcp_readiness_notification_receipt import MCPReadinessNotificationReceipt
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: MCPReadinessNotificationAction,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mcp-readiness-notifications/actions".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessNotificationReceipt | None:
    if response.status_code == 200:
        response_200 = MCPReadinessNotificationReceipt.from_dict(response.json())

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
) -> Response[Any | MCPReadinessNotificationReceipt]:
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
    body: MCPReadinessNotificationAction,
) -> Response[Any | MCPReadinessNotificationReceipt]:
    """Retry or abandon a readiness receiver or event

     Admin scope and MFA required. With destination supplied the action changes only that receiver head
    and preserves other receivers. Omit destination to act on the oldest whole event. Retry makes a
    failed or expired-lease head eligible on the next evaluator pass. Abandon permanently stops the
    selected deliveries while retaining incident history. Actions recheck the selected queue order and
    lease plus rule enablement and maintenance. The shared event version fences stale requests. Reusing
    a request ID with the identical payload including destination returns the durable receipt; changed
    payload conflicts. A receipt confirms the decision rather than webhook delivery.

    Args:
        slug (str):
        body (MCPReadinessNotificationAction): Version-fenced decision against a receiver head or
            the oldest event using an app-scoped idempotency key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessNotificationReceipt]
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
    body: MCPReadinessNotificationAction,
) -> Any | MCPReadinessNotificationReceipt | None:
    """Retry or abandon a readiness receiver or event

     Admin scope and MFA required. With destination supplied the action changes only that receiver head
    and preserves other receivers. Omit destination to act on the oldest whole event. Retry makes a
    failed or expired-lease head eligible on the next evaluator pass. Abandon permanently stops the
    selected deliveries while retaining incident history. Actions recheck the selected queue order and
    lease plus rule enablement and maintenance. The shared event version fences stale requests. Reusing
    a request ID with the identical payload including destination returns the durable receipt; changed
    payload conflicts. A receipt confirms the decision rather than webhook delivery.

    Args:
        slug (str):
        body (MCPReadinessNotificationAction): Version-fenced decision against a receiver head or
            the oldest event using an app-scoped idempotency key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessNotificationReceipt
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
    body: MCPReadinessNotificationAction,
) -> Response[Any | MCPReadinessNotificationReceipt]:
    """Retry or abandon a readiness receiver or event

     Admin scope and MFA required. With destination supplied the action changes only that receiver head
    and preserves other receivers. Omit destination to act on the oldest whole event. Retry makes a
    failed or expired-lease head eligible on the next evaluator pass. Abandon permanently stops the
    selected deliveries while retaining incident history. Actions recheck the selected queue order and
    lease plus rule enablement and maintenance. The shared event version fences stale requests. Reusing
    a request ID with the identical payload including destination returns the durable receipt; changed
    payload conflicts. A receipt confirms the decision rather than webhook delivery.

    Args:
        slug (str):
        body (MCPReadinessNotificationAction): Version-fenced decision against a receiver head or
            the oldest event using an app-scoped idempotency key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessNotificationReceipt]
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
    body: MCPReadinessNotificationAction,
) -> Any | MCPReadinessNotificationReceipt | None:
    """Retry or abandon a readiness receiver or event

     Admin scope and MFA required. With destination supplied the action changes only that receiver head
    and preserves other receivers. Omit destination to act on the oldest whole event. Retry makes a
    failed or expired-lease head eligible on the next evaluator pass. Abandon permanently stops the
    selected deliveries while retaining incident history. Actions recheck the selected queue order and
    lease plus rule enablement and maintenance. The shared event version fences stale requests. Reusing
    a request ID with the identical payload including destination returns the durable receipt; changed
    payload conflicts. A receipt confirms the decision rather than webhook delivery.

    Args:
        slug (str):
        body (MCPReadinessNotificationAction): Version-fenced decision against a receiver head or
            the oldest event using an app-scoped idempotency key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessNotificationReceipt
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
