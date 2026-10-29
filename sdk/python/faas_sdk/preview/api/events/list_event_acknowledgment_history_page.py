import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_acknowledgment_history_page import EventAcknowledgmentHistoryPage
from ...models.list_event_acknowledgment_history_page_kind import (
    ListEventAcknowledgmentHistoryPageKind,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    invocation_id: UUID | Unset = UNSET,
    kind: ListEventAcknowledgmentHistoryPageKind | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_invocation_id: str | Unset = UNSET
    if not isinstance(invocation_id, Unset):
        json_invocation_id = str(invocation_id)
    params["invocation_id"] = json_invocation_id

    json_kind: str | Unset = UNSET
    if not isinstance(kind, Unset):
        json_kind = kind

    params["kind"] = json_kind

    json_from_: str | Unset = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/acknowledgment-history/pages".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventAcknowledgmentHistoryPage | Problem | None:
    if response.status_code == 200:
        response_200 = EventAcknowledgmentHistoryPage.from_dict(response.json())

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
) -> Response[EventAcknowledgmentHistoryPage | Problem]:
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
    invocation_id: UUID | Unset = UNSET,
    kind: ListEventAcknowledgmentHistoryPageKind | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> Response[EventAcknowledgmentHistoryPage | Problem]:
    """Page through retained acknowledgment outcome groups.

     Requires apps:read or admin. Orders immutable first occurrence and identity fields descending so
    renewals cannot move groups between pages. A fixed first-page cutoff excludes later-created groups.
    Counts and last_at remain live; deletion, retention and initially uncommitted inserts prevent a
    database snapshot guarantee. Filters select groups by first_at, not individual occurrences. Repeat
    all filters and the page size with the cursor. Cursors expire one hour after the cutoff and grant no
    authority. The existing acknowledgment-history operation keeps its latest-update ordering.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):
        kind (ListEventAcknowledgmentHistoryPageKind | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentHistoryPage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        invocation_id=invocation_id,
        kind=kind,
        from_=from_,
        until=until,
        limit=limit,
        cursor=cursor,
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
    invocation_id: UUID | Unset = UNSET,
    kind: ListEventAcknowledgmentHistoryPageKind | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> EventAcknowledgmentHistoryPage | Problem | None:
    """Page through retained acknowledgment outcome groups.

     Requires apps:read or admin. Orders immutable first occurrence and identity fields descending so
    renewals cannot move groups between pages. A fixed first-page cutoff excludes later-created groups.
    Counts and last_at remain live; deletion, retention and initially uncommitted inserts prevent a
    database snapshot guarantee. Filters select groups by first_at, not individual occurrences. Repeat
    all filters and the page size with the cursor. Cursors expire one hour after the cutoff and grant no
    authority. The existing acknowledgment-history operation keeps its latest-update ordering.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):
        kind (ListEventAcknowledgmentHistoryPageKind | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentHistoryPage | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        invocation_id=invocation_id,
        kind=kind,
        from_=from_,
        until=until,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    invocation_id: UUID | Unset = UNSET,
    kind: ListEventAcknowledgmentHistoryPageKind | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> Response[EventAcknowledgmentHistoryPage | Problem]:
    """Page through retained acknowledgment outcome groups.

     Requires apps:read or admin. Orders immutable first occurrence and identity fields descending so
    renewals cannot move groups between pages. A fixed first-page cutoff excludes later-created groups.
    Counts and last_at remain live; deletion, retention and initially uncommitted inserts prevent a
    database snapshot guarantee. Filters select groups by first_at, not individual occurrences. Repeat
    all filters and the page size with the cursor. Cursors expire one hour after the cutoff and grant no
    authority. The existing acknowledgment-history operation keeps its latest-update ordering.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):
        kind (ListEventAcknowledgmentHistoryPageKind | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentHistoryPage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        invocation_id=invocation_id,
        kind=kind,
        from_=from_,
        until=until,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    invocation_id: UUID | Unset = UNSET,
    kind: ListEventAcknowledgmentHistoryPageKind | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> EventAcknowledgmentHistoryPage | Problem | None:
    """Page through retained acknowledgment outcome groups.

     Requires apps:read or admin. Orders immutable first occurrence and identity fields descending so
    renewals cannot move groups between pages. A fixed first-page cutoff excludes later-created groups.
    Counts and last_at remain live; deletion, retention and initially uncommitted inserts prevent a
    database snapshot guarantee. Filters select groups by first_at, not individual occurrences. Repeat
    all filters and the page size with the cursor. Cursors expire one hour after the cutoff and grant no
    authority. The existing acknowledgment-history operation keeps its latest-update ordering.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):
        kind (ListEventAcknowledgmentHistoryPageKind | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentHistoryPage | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            invocation_id=invocation_id,
            kind=kind,
            from_=from_,
            until=until,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
