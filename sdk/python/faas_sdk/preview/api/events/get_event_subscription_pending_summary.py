from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_pending_summary import EventSubscriptionPendingSummary
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-control/pending-summary".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionPendingSummary | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionPendingSummary.from_dict(response.json())

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
) -> Response[EventSubscriptionPendingSummary | Problem]:
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
) -> Response[EventSubscriptionPendingSummary | Problem]:
    """Read complete consumer pending counts and effective deadlines.

     Read-only, payload-free counts and earliest effective deadline across all retained captured pending
    application recipients for this subscription. One Repeatable Read snapshot includes current
    controls. Processing work, future arrivals and publications awaiting recipient capture are excluded.
    No sample cap; request and statement budgets bound scan time. Timeout returns 504 without partial
    counts. Incomplete or absent evidence is unknown, never zero or a successful recovery. Counts do not
    model future admission or guarantee deadline safety.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionPendingSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
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
) -> EventSubscriptionPendingSummary | Problem | None:
    """Read complete consumer pending counts and effective deadlines.

     Read-only, payload-free counts and earliest effective deadline across all retained captured pending
    application recipients for this subscription. One Repeatable Read snapshot includes current
    controls. Processing work, future arrivals and publications awaiting recipient capture are excluded.
    No sample cap; request and statement budgets bound scan time. Timeout returns 504 without partial
    counts. Incomplete or absent evidence is unknown, never zero or a successful recovery. Counts do not
    model future admission or guarantee deadline safety.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionPendingSummary | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventSubscriptionPendingSummary | Problem]:
    """Read complete consumer pending counts and effective deadlines.

     Read-only, payload-free counts and earliest effective deadline across all retained captured pending
    application recipients for this subscription. One Repeatable Read snapshot includes current
    controls. Processing work, future arrivals and publications awaiting recipient capture are excluded.
    No sample cap; request and statement budgets bound scan time. Timeout returns 504 without partial
    counts. Incomplete or absent evidence is unknown, never zero or a successful recovery. Counts do not
    model future admission or guarantee deadline safety.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionPendingSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventSubscriptionPendingSummary | Problem | None:
    """Read complete consumer pending counts and effective deadlines.

     Read-only, payload-free counts and earliest effective deadline across all retained captured pending
    application recipients for this subscription. One Repeatable Read snapshot includes current
    controls. Processing work, future arrivals and publications awaiting recipient capture are excluded.
    No sample cap; request and statement budgets bound scan time. Timeout returns 504 without partial
    counts. Incomplete or absent evidence is unknown, never zero or a successful recovery. Counts do not
    model future admission or guarantee deadline safety.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionPendingSummary | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
        )
    ).parsed
