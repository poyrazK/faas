import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_replay_preview_response import EventReplayPreviewResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_until = until.isoformat()
    params["until"] = json_until

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/replay-preview".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReplayPreviewResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventReplayPreviewResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventReplayPreviewResponse | Problem]:
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
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventReplayPreviewResponse | Problem]:
    """Preview historical retained events for one subscription.

     Read-only, account-scoped preview using the current enabled ordinary
    application subscription and the routing matcher. Work-bound subscriptions
    are unsupported. Select a half-open platform acceptance-time range; producer
    event time is not the range key. A fixed cutoff excludes later acceptances.
    Each page examines at most limit envelopes, so a page can contain no matches
    and still have next_after. Counts describe this page only. Pass the cursor
    unchanged with the same target and range; a changed declaration returns 409.
    Matches contain metadata and original captured-membership status, never
    payloads. Captured membership does not imply successful delivery or define
    replay eligibility. No deliveries, replay jobs or retention pins are created.
    Retention can remove rows between pages. This is a live retained view, not a
    frozen export or a complete archive; late commits can change visible rows.
    Settled receipts retain 30 days from routing settlement; pending receipts may
    survive longer. Earliest retained acceptance is account-wide and does not
    establish coverage of a requested range. Responses use Cache-Control: no-store
    and require the existing read scopes and MFA rules.

    Args:
        slug (str):
        subscription_id (UUID):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
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
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventReplayPreviewResponse | Problem | None:
    """Preview historical retained events for one subscription.

     Read-only, account-scoped preview using the current enabled ordinary
    application subscription and the routing matcher. Work-bound subscriptions
    are unsupported. Select a half-open platform acceptance-time range; producer
    event time is not the range key. A fixed cutoff excludes later acceptances.
    Each page examines at most limit envelopes, so a page can contain no matches
    and still have next_after. Counts describe this page only. Pass the cursor
    unchanged with the same target and range; a changed declaration returns 409.
    Matches contain metadata and original captured-membership status, never
    payloads. Captured membership does not imply successful delivery or define
    replay eligibility. No deliveries, replay jobs or retention pins are created.
    Retention can remove rows between pages. This is a live retained view, not a
    frozen export or a complete archive; late commits can change visible rows.
    Settled receipts retain 30 days from routing settlement; pending receipts may
    survive longer. Earliest retained acceptance is account-wide and does not
    establish coverage of a requested range. Responses use Cache-Control: no-store
    and require the existing read scopes and MFA rules.

    Args:
        slug (str):
        subscription_id (UUID):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayPreviewResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventReplayPreviewResponse | Problem]:
    """Preview historical retained events for one subscription.

     Read-only, account-scoped preview using the current enabled ordinary
    application subscription and the routing matcher. Work-bound subscriptions
    are unsupported. Select a half-open platform acceptance-time range; producer
    event time is not the range key. A fixed cutoff excludes later acceptances.
    Each page examines at most limit envelopes, so a page can contain no matches
    and still have next_after. Counts describe this page only. Pass the cursor
    unchanged with the same target and range; a changed declaration returns 409.
    Matches contain metadata and original captured-membership status, never
    payloads. Captured membership does not imply successful delivery or define
    replay eligibility. No deliveries, replay jobs or retention pins are created.
    Retention can remove rows between pages. This is a live retained view, not a
    frozen export or a complete archive; late commits can change visible rows.
    Settled receipts retain 30 days from routing settlement; pending receipts may
    survive longer. Earliest retained acceptance is account-wide and does not
    establish coverage of a requested range. Responses use Cache-Control: no-store
    and require the existing read scopes and MFA rules.

    Args:
        slug (str):
        subscription_id (UUID):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    from_: datetime.datetime,
    until: datetime.datetime,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventReplayPreviewResponse | Problem | None:
    """Preview historical retained events for one subscription.

     Read-only, account-scoped preview using the current enabled ordinary
    application subscription and the routing matcher. Work-bound subscriptions
    are unsupported. Select a half-open platform acceptance-time range; producer
    event time is not the range key. A fixed cutoff excludes later acceptances.
    Each page examines at most limit envelopes, so a page can contain no matches
    and still have next_after. Counts describe this page only. Pass the cursor
    unchanged with the same target and range; a changed declaration returns 409.
    Matches contain metadata and original captured-membership status, never
    payloads. Captured membership does not imply successful delivery or define
    replay eligibility. No deliveries, replay jobs or retention pins are created.
    Retention can remove rows between pages. This is a live retained view, not a
    frozen export or a complete archive; late commits can change visible rows.
    Settled receipts retain 30 days from routing settlement; pending receipts may
    survive longer. Earliest retained acceptance is account-wide and does not
    establish coverage of a requested range. Responses use Cache-Control: no-store
    and require the existing read scopes and MFA rules.

    Args:
        slug (str):
        subscription_id (UUID):
        from_ (datetime.datetime):
        until (datetime.datetime):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayPreviewResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            from_=from_,
            until=until,
            after=after,
            limit=limit,
        )
    ).parsed
