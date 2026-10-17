import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_deadline_page import EventSubscriptionDeadlinePage
from ...models.list_event_subscription_deadlines_status import (
    ListEventSubscriptionDeadlinesStatus,
)
from ...models.list_event_subscription_deadlines_waiting_reason import (
    ListEventSubscriptionDeadlinesWaitingReason,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    deadline_from: datetime.datetime | Unset = UNSET,
    deadline_before: datetime.datetime | Unset = UNSET,
    status: ListEventSubscriptionDeadlinesStatus | Unset = "all",
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
    within_millis: int | Unset = UNSET,
    waiting_reason: ListEventSubscriptionDeadlinesWaitingReason | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_deadline_from: str | Unset = UNSET
    if not isinstance(deadline_from, Unset):
        json_deadline_from = deadline_from.isoformat()
    params["deadline_from"] = json_deadline_from

    json_deadline_before: str | Unset = UNSET
    if not isinstance(deadline_before, Unset):
        json_deadline_before = deadline_before.isoformat()
    params["deadline_before"] = json_deadline_before

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["cursor"] = cursor

    params["limit"] = limit

    params["within_millis"] = within_millis

    json_waiting_reason: str | Unset = UNSET
    if not isinstance(waiting_reason, Unset):
        json_waiting_reason = waiting_reason

    params["waiting_reason"] = json_waiting_reason

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-control/deadlines".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionDeadlinePage | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionDeadlinePage.from_dict(response.json())

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
) -> Response[EventSubscriptionDeadlinePage | Problem]:
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
    deadline_from: datetime.datetime | Unset = UNSET,
    deadline_before: datetime.datetime | Unset = UNSET,
    status: ListEventSubscriptionDeadlinesStatus | Unset = "all",
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
    within_millis: int | Unset = UNSET,
    waiting_reason: ListEventSubscriptionDeadlinesWaitingReason | Unset = UNSET,
) -> Response[EventSubscriptionDeadlinePage | Problem]:
    """List pending deliveries by effective deadline.

     Read-only deadline triage for retained captured pending application recipients of one owned
    subscription, without payloads. Orders by effective deadline, acceptance time and internal receipt
    ID ascending; this does not guarantee delivery order. Optional deadline_from is inclusive and
    deadline_before exclusive. Expired/upcoming filters compare to the first page window_at, which also
    bounds acceptance time. Each page has a fresh observation and live routing state, so expired flags
    and waiting reasons use observed_at. Cursor is scoped to account, application, subscription,
    normalized filters and page size, expires after 15 minutes, and is navigational metadata rather than
    authorization. Membership and deadlines can change between pages; newly captured or replayed work
    can appear, disappear, move or be skipped. This is not a frozen snapshot, reservation or deadline-
    safety certificate. No effective deadline means exclusion. Reads have fixed time budgets; timeout
    returns no partial page.

    Args:
        slug (str):
        subscription_id (UUID):
        deadline_from (datetime.datetime | Unset):
        deadline_before (datetime.datetime | Unset):
        status (ListEventSubscriptionDeadlinesStatus | Unset):  Default: 'all'.
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.
        within_millis (int | Unset):
        waiting_reason (ListEventSubscriptionDeadlinesWaitingReason | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionDeadlinePage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        deadline_from=deadline_from,
        deadline_before=deadline_before,
        status=status,
        cursor=cursor,
        limit=limit,
        within_millis=within_millis,
        waiting_reason=waiting_reason,
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
    deadline_from: datetime.datetime | Unset = UNSET,
    deadline_before: datetime.datetime | Unset = UNSET,
    status: ListEventSubscriptionDeadlinesStatus | Unset = "all",
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
    within_millis: int | Unset = UNSET,
    waiting_reason: ListEventSubscriptionDeadlinesWaitingReason | Unset = UNSET,
) -> EventSubscriptionDeadlinePage | Problem | None:
    """List pending deliveries by effective deadline.

     Read-only deadline triage for retained captured pending application recipients of one owned
    subscription, without payloads. Orders by effective deadline, acceptance time and internal receipt
    ID ascending; this does not guarantee delivery order. Optional deadline_from is inclusive and
    deadline_before exclusive. Expired/upcoming filters compare to the first page window_at, which also
    bounds acceptance time. Each page has a fresh observation and live routing state, so expired flags
    and waiting reasons use observed_at. Cursor is scoped to account, application, subscription,
    normalized filters and page size, expires after 15 minutes, and is navigational metadata rather than
    authorization. Membership and deadlines can change between pages; newly captured or replayed work
    can appear, disappear, move or be skipped. This is not a frozen snapshot, reservation or deadline-
    safety certificate. No effective deadline means exclusion. Reads have fixed time budgets; timeout
    returns no partial page.

    Args:
        slug (str):
        subscription_id (UUID):
        deadline_from (datetime.datetime | Unset):
        deadline_before (datetime.datetime | Unset):
        status (ListEventSubscriptionDeadlinesStatus | Unset):  Default: 'all'.
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.
        within_millis (int | Unset):
        waiting_reason (ListEventSubscriptionDeadlinesWaitingReason | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionDeadlinePage | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        deadline_from=deadline_from,
        deadline_before=deadline_before,
        status=status,
        cursor=cursor,
        limit=limit,
        within_millis=within_millis,
        waiting_reason=waiting_reason,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    deadline_from: datetime.datetime | Unset = UNSET,
    deadline_before: datetime.datetime | Unset = UNSET,
    status: ListEventSubscriptionDeadlinesStatus | Unset = "all",
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
    within_millis: int | Unset = UNSET,
    waiting_reason: ListEventSubscriptionDeadlinesWaitingReason | Unset = UNSET,
) -> Response[EventSubscriptionDeadlinePage | Problem]:
    """List pending deliveries by effective deadline.

     Read-only deadline triage for retained captured pending application recipients of one owned
    subscription, without payloads. Orders by effective deadline, acceptance time and internal receipt
    ID ascending; this does not guarantee delivery order. Optional deadline_from is inclusive and
    deadline_before exclusive. Expired/upcoming filters compare to the first page window_at, which also
    bounds acceptance time. Each page has a fresh observation and live routing state, so expired flags
    and waiting reasons use observed_at. Cursor is scoped to account, application, subscription,
    normalized filters and page size, expires after 15 minutes, and is navigational metadata rather than
    authorization. Membership and deadlines can change between pages; newly captured or replayed work
    can appear, disappear, move or be skipped. This is not a frozen snapshot, reservation or deadline-
    safety certificate. No effective deadline means exclusion. Reads have fixed time budgets; timeout
    returns no partial page.

    Args:
        slug (str):
        subscription_id (UUID):
        deadline_from (datetime.datetime | Unset):
        deadline_before (datetime.datetime | Unset):
        status (ListEventSubscriptionDeadlinesStatus | Unset):  Default: 'all'.
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.
        within_millis (int | Unset):
        waiting_reason (ListEventSubscriptionDeadlinesWaitingReason | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionDeadlinePage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        deadline_from=deadline_from,
        deadline_before=deadline_before,
        status=status,
        cursor=cursor,
        limit=limit,
        within_millis=within_millis,
        waiting_reason=waiting_reason,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    deadline_from: datetime.datetime | Unset = UNSET,
    deadline_before: datetime.datetime | Unset = UNSET,
    status: ListEventSubscriptionDeadlinesStatus | Unset = "all",
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
    within_millis: int | Unset = UNSET,
    waiting_reason: ListEventSubscriptionDeadlinesWaitingReason | Unset = UNSET,
) -> EventSubscriptionDeadlinePage | Problem | None:
    """List pending deliveries by effective deadline.

     Read-only deadline triage for retained captured pending application recipients of one owned
    subscription, without payloads. Orders by effective deadline, acceptance time and internal receipt
    ID ascending; this does not guarantee delivery order. Optional deadline_from is inclusive and
    deadline_before exclusive. Expired/upcoming filters compare to the first page window_at, which also
    bounds acceptance time. Each page has a fresh observation and live routing state, so expired flags
    and waiting reasons use observed_at. Cursor is scoped to account, application, subscription,
    normalized filters and page size, expires after 15 minutes, and is navigational metadata rather than
    authorization. Membership and deadlines can change between pages; newly captured or replayed work
    can appear, disappear, move or be skipped. This is not a frozen snapshot, reservation or deadline-
    safety certificate. No effective deadline means exclusion. Reads have fixed time budgets; timeout
    returns no partial page.

    Args:
        slug (str):
        subscription_id (UUID):
        deadline_from (datetime.datetime | Unset):
        deadline_before (datetime.datetime | Unset):
        status (ListEventSubscriptionDeadlinesStatus | Unset):  Default: 'all'.
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.
        within_millis (int | Unset):
        waiting_reason (ListEventSubscriptionDeadlinesWaitingReason | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionDeadlinePage | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            deadline_from=deadline_from,
            deadline_before=deadline_before,
            status=status,
            cursor=cursor,
            limit=limit,
            within_millis=within_millis,
            waiting_reason=waiting_reason,
        )
    ).parsed
