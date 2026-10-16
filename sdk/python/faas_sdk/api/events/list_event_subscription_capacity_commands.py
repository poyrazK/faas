from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_capacity_commands import EventSubscriptionCapacityCommands
from ...models.list_event_subscription_capacity_commands_outcome import (
    ListEventSubscriptionCapacityCommandsOutcome,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    outcome: ListEventSubscriptionCapacityCommandsOutcome | Unset = "any",
    limit: int | Unset = 25,
    after: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_outcome: str | Unset = UNSET
    if not isinstance(outcome, Unset):
        json_outcome = outcome

    params["outcome"] = json_outcome

    params["limit"] = limit

    params["after"] = after

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-control/capacity/commands".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionCapacityCommands | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionCapacityCommands.from_dict(response.json())

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
) -> Response[EventSubscriptionCapacityCommands | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    outcome: ListEventSubscriptionCapacityCommandsOutcome | Unset = "any",
    limit: int | Unset = 25,
    after: str | Unset = UNSET,
) -> Response[EventSubscriptionCapacityCommands | Problem]:
    """List retained original capacity-command receipts.

     Requires apps:read or admin and MFA. Read-only newest-first pagination by recorded_at and command
    UUID descending. Outcome and expiry filters apply before the page limit. Each page has a fresh
    ownership and receipt snapshot; cursors are bound to account, app, subscription and normalized
    outcome filter, but do not freeze history. New commands and expiry can affect later pages. Listing
    never applies commands, prunes evidence or extends retention. Empty history does not prove commands
    never ran. Recording order does not establish delivery or execution order. Responses use Cache-
    Control no-store.

    Args:
        slug (str):
        subscription_id (UUID):
        outcome (ListEventSubscriptionCapacityCommandsOutcome | Unset):  Default: 'any'.
        limit (int | Unset):  Default: 25.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionCapacityCommands | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        outcome=outcome,
        limit=limit,
        after=after,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    outcome: ListEventSubscriptionCapacityCommandsOutcome | Unset = "any",
    limit: int | Unset = 25,
    after: str | Unset = UNSET,
) -> EventSubscriptionCapacityCommands | Problem | None:
    """List retained original capacity-command receipts.

     Requires apps:read or admin and MFA. Read-only newest-first pagination by recorded_at and command
    UUID descending. Outcome and expiry filters apply before the page limit. Each page has a fresh
    ownership and receipt snapshot; cursors are bound to account, app, subscription and normalized
    outcome filter, but do not freeze history. New commands and expiry can affect later pages. Listing
    never applies commands, prunes evidence or extends retention. Empty history does not prove commands
    never ran. Recording order does not establish delivery or execution order. Responses use Cache-
    Control no-store.

    Args:
        slug (str):
        subscription_id (UUID):
        outcome (ListEventSubscriptionCapacityCommandsOutcome | Unset):  Default: 'any'.
        limit (int | Unset):  Default: 25.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionCapacityCommands | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        outcome=outcome,
        limit=limit,
        after=after,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    outcome: ListEventSubscriptionCapacityCommandsOutcome | Unset = "any",
    limit: int | Unset = 25,
    after: str | Unset = UNSET,
) -> Response[EventSubscriptionCapacityCommands | Problem]:
    """List retained original capacity-command receipts.

     Requires apps:read or admin and MFA. Read-only newest-first pagination by recorded_at and command
    UUID descending. Outcome and expiry filters apply before the page limit. Each page has a fresh
    ownership and receipt snapshot; cursors are bound to account, app, subscription and normalized
    outcome filter, but do not freeze history. New commands and expiry can affect later pages. Listing
    never applies commands, prunes evidence or extends retention. Empty history does not prove commands
    never ran. Recording order does not establish delivery or execution order. Responses use Cache-
    Control no-store.

    Args:
        slug (str):
        subscription_id (UUID):
        outcome (ListEventSubscriptionCapacityCommandsOutcome | Unset):  Default: 'any'.
        limit (int | Unset):  Default: 25.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionCapacityCommands | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        outcome=outcome,
        limit=limit,
        after=after,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    outcome: ListEventSubscriptionCapacityCommandsOutcome | Unset = "any",
    limit: int | Unset = 25,
    after: str | Unset = UNSET,
) -> EventSubscriptionCapacityCommands | Problem | None:
    """List retained original capacity-command receipts.

     Requires apps:read or admin and MFA. Read-only newest-first pagination by recorded_at and command
    UUID descending. Outcome and expiry filters apply before the page limit. Each page has a fresh
    ownership and receipt snapshot; cursors are bound to account, app, subscription and normalized
    outcome filter, but do not freeze history. New commands and expiry can affect later pages. Listing
    never applies commands, prunes evidence or extends retention. Empty history does not prove commands
    never ran. Recording order does not establish delivery or execution order. Responses use Cache-
    Control no-store.

    Args:
        slug (str):
        subscription_id (UUID):
        outcome (ListEventSubscriptionCapacityCommandsOutcome | Unset):  Default: 'any'.
        limit (int | Unset):  Default: 25.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionCapacityCommands | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            outcome=outcome,
            limit=limit,
            after=after,
        )
    ).parsed
