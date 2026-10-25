from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_capacity_command_receipt import EventSubscriptionCapacityCommandReceipt
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    command_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-control/capacity/commands/{command_id}".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
            command_id=quote(str(command_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionCapacityCommandReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionCapacityCommandReceipt.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventSubscriptionCapacityCommandReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    command_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventSubscriptionCapacityCommandReceipt | Problem]:
    """Read retained original capacity-command evidence.

     Requires apps:read or admin and MFA. Returns the original successful command request values and
    outcome without applying a change, consuming receipt capacity, pruning records or extending
    retention. The fixed retention deadline is thirty days after success. A missing or expired receipt
    is unavailable evidence and does not prove the command never ran. The receipt does not describe
    current configuration and does not establish an ordering or execution guarantee.

    Args:
        slug (str):
        subscription_id (UUID):
        command_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionCapacityCommandReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        command_id=command_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    command_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventSubscriptionCapacityCommandReceipt | Problem | None:
    """Read retained original capacity-command evidence.

     Requires apps:read or admin and MFA. Returns the original successful command request values and
    outcome without applying a change, consuming receipt capacity, pruning records or extending
    retention. The fixed retention deadline is thirty days after success. A missing or expired receipt
    is unavailable evidence and does not prove the command never ran. The receipt does not describe
    current configuration and does not establish an ordering or execution guarantee.

    Args:
        slug (str):
        subscription_id (UUID):
        command_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionCapacityCommandReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        command_id=command_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    command_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventSubscriptionCapacityCommandReceipt | Problem]:
    """Read retained original capacity-command evidence.

     Requires apps:read or admin and MFA. Returns the original successful command request values and
    outcome without applying a change, consuming receipt capacity, pruning records or extending
    retention. The fixed retention deadline is thirty days after success. A missing or expired receipt
    is unavailable evidence and does not prove the command never ran. The receipt does not describe
    current configuration and does not establish an ordering or execution guarantee.

    Args:
        slug (str):
        subscription_id (UUID):
        command_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionCapacityCommandReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        command_id=command_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    command_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventSubscriptionCapacityCommandReceipt | Problem | None:
    """Read retained original capacity-command evidence.

     Requires apps:read or admin and MFA. Returns the original successful command request values and
    outcome without applying a change, consuming receipt capacity, pruning records or extending
    retention. The fixed retention deadline is thirty days after success. A missing or expired receipt
    is unavailable evidence and does not prove the command never ran. The receipt does not describe
    current configuration and does not establish an ordering or execution guarantee.

    Args:
        slug (str):
        subscription_id (UUID):
        command_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionCapacityCommandReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            command_id=command_id,
            client=client,
        )
    ).parsed
