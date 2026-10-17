from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_deadline_window import EventSubscriptionDeadlineWindow
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    within_millis: int,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["within_millis"] = within_millis

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-control/deadline-window".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionDeadlineWindow | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionDeadlineWindow.from_dict(response.json())

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
) -> Response[EventSubscriptionDeadlineWindow | Problem]:
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
    within_millis: int,
) -> Response[EventSubscriptionDeadlineWindow | Problem]:
    """Count upcoming delivery deadlines and observed waiting reasons.

     Read-only, payload-free full pending summary and deadline cohorts from one Repeatable Read snapshot.
    Upcoming deadlines are strictly after summary.observed_at and strictly before window_end_at.
    Already-expired recipients are separate in summary.already_expired_count. Each cohort has an exact
    partition of observed backlog waiting reasons, including zero keys. Counts have no sample cap and
    include only retained captured pending application recipients with effective deadlines; processing,
    future arrivals and recipients without deadlines are excluded from cohorts. Waiting reasons are
    observations, not unique causes or execution guarantees. Fixed request and statement budgets apply;
    timeout returns no partial counts. Require summary.complete before interpreting coverage or
    recovery. This does not predict admissions, change state, reserve capacity or extend deadlines.

    Args:
        slug (str):
        subscription_id (UUID):
        within_millis (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionDeadlineWindow | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        within_millis=within_millis,
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
    within_millis: int,
) -> EventSubscriptionDeadlineWindow | Problem | None:
    """Count upcoming delivery deadlines and observed waiting reasons.

     Read-only, payload-free full pending summary and deadline cohorts from one Repeatable Read snapshot.
    Upcoming deadlines are strictly after summary.observed_at and strictly before window_end_at.
    Already-expired recipients are separate in summary.already_expired_count. Each cohort has an exact
    partition of observed backlog waiting reasons, including zero keys. Counts have no sample cap and
    include only retained captured pending application recipients with effective deadlines; processing,
    future arrivals and recipients without deadlines are excluded from cohorts. Waiting reasons are
    observations, not unique causes or execution guarantees. Fixed request and statement budgets apply;
    timeout returns no partial counts. Require summary.complete before interpreting coverage or
    recovery. This does not predict admissions, change state, reserve capacity or extend deadlines.

    Args:
        slug (str):
        subscription_id (UUID):
        within_millis (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionDeadlineWindow | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        within_millis=within_millis,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    within_millis: int,
) -> Response[EventSubscriptionDeadlineWindow | Problem]:
    """Count upcoming delivery deadlines and observed waiting reasons.

     Read-only, payload-free full pending summary and deadline cohorts from one Repeatable Read snapshot.
    Upcoming deadlines are strictly after summary.observed_at and strictly before window_end_at.
    Already-expired recipients are separate in summary.already_expired_count. Each cohort has an exact
    partition of observed backlog waiting reasons, including zero keys. Counts have no sample cap and
    include only retained captured pending application recipients with effective deadlines; processing,
    future arrivals and recipients without deadlines are excluded from cohorts. Waiting reasons are
    observations, not unique causes or execution guarantees. Fixed request and statement budgets apply;
    timeout returns no partial counts. Require summary.complete before interpreting coverage or
    recovery. This does not predict admissions, change state, reserve capacity or extend deadlines.

    Args:
        slug (str):
        subscription_id (UUID):
        within_millis (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionDeadlineWindow | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        within_millis=within_millis,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    within_millis: int,
) -> EventSubscriptionDeadlineWindow | Problem | None:
    """Count upcoming delivery deadlines and observed waiting reasons.

     Read-only, payload-free full pending summary and deadline cohorts from one Repeatable Read snapshot.
    Upcoming deadlines are strictly after summary.observed_at and strictly before window_end_at.
    Already-expired recipients are separate in summary.already_expired_count. Each cohort has an exact
    partition of observed backlog waiting reasons, including zero keys. Counts have no sample cap and
    include only retained captured pending application recipients with effective deadlines; processing,
    future arrivals and recipients without deadlines are excluded from cohorts. Waiting reasons are
    observations, not unique causes or execution guarantees. Fixed request and statement budgets apply;
    timeout returns no partial counts. Require summary.complete before interpreting coverage or
    recovery. This does not predict admissions, change state, reserve capacity or extend deadlines.

    Args:
        slug (str):
        subscription_id (UUID):
        within_millis (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionDeadlineWindow | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            within_millis=within_millis,
        )
    ).parsed
