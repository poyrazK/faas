import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_wake_timeline_response import AppWakeTimelineResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/wake-timeline".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppWakeTimelineResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppWakeTimelineResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppWakeTimelineResponse | Problem]:
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[AppWakeTimelineResponse | Problem]:
    """Per-app wake timeline (JSON mirror of the dashboard page).

     Wire-friendly mirror of the dashboard's per-app wake-timeline
    HTML page (`/dashboard/apps/{slug}/wake-timeline`,
    cmd/apid/handlers_dashboard.go:2548 renderAppWakeTimeline).
    The HTML page keeps its pre-rendered HTML chips; this endpoint
    emits the same data as JSON so a separate frontend agent can
    render without re-parsing HTML.

    Returns the 50 most-recent instance rows for the app, joined
    against the events table's `wake.boot_started` rows for the
    per-row telemetry (Trigger, QueuedCount, ConcurrencyAtAdmit,
    AtCapacity, ReadyInMS). The aggregation math is shared with
    the HTML page:

      - 24h cutoff descending-break: the moment a row's
        `started_at` falls before the trailing-24h instant, the
        loop breaks (no further iteration). Pre-ADR-123 fleet
        rows with no `started_at` are not eligible for the break
        (always in scope).
      - Two-denominator rule for `at_capacity_pct`: numerator is
        the count of rows where the events join succeeded AND
        the at_capacity flag is true; denominator is the count
        of rows where the events join succeeded
        (`wake_count_with_meta`). Pre-PR-A fleet rows contribute
        to `wake_count_24h` but not the denominator — same
        posture as the HTML page.

    Plan-gated Hobby+ (mirror of /v1/apps/{slug}/metrics —
    same `code` so a downgrade between the two endpoints flips
    both at once).

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWakeTimelineResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> AppWakeTimelineResponse | Problem | None:
    """Per-app wake timeline (JSON mirror of the dashboard page).

     Wire-friendly mirror of the dashboard's per-app wake-timeline
    HTML page (`/dashboard/apps/{slug}/wake-timeline`,
    cmd/apid/handlers_dashboard.go:2548 renderAppWakeTimeline).
    The HTML page keeps its pre-rendered HTML chips; this endpoint
    emits the same data as JSON so a separate frontend agent can
    render without re-parsing HTML.

    Returns the 50 most-recent instance rows for the app, joined
    against the events table's `wake.boot_started` rows for the
    per-row telemetry (Trigger, QueuedCount, ConcurrencyAtAdmit,
    AtCapacity, ReadyInMS). The aggregation math is shared with
    the HTML page:

      - 24h cutoff descending-break: the moment a row's
        `started_at` falls before the trailing-24h instant, the
        loop breaks (no further iteration). Pre-ADR-123 fleet
        rows with no `started_at` are not eligible for the break
        (always in scope).
      - Two-denominator rule for `at_capacity_pct`: numerator is
        the count of rows where the events join succeeded AND
        the at_capacity flag is true; denominator is the count
        of rows where the events join succeeded
        (`wake_count_with_meta`). Pre-PR-A fleet rows contribute
        to `wake_count_24h` but not the denominator — same
        posture as the HTML page.

    Plan-gated Hobby+ (mirror of /v1/apps/{slug}/metrics —
    same `code` so a downgrade between the two endpoints flips
    both at once).

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWakeTimelineResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[AppWakeTimelineResponse | Problem]:
    """Per-app wake timeline (JSON mirror of the dashboard page).

     Wire-friendly mirror of the dashboard's per-app wake-timeline
    HTML page (`/dashboard/apps/{slug}/wake-timeline`,
    cmd/apid/handlers_dashboard.go:2548 renderAppWakeTimeline).
    The HTML page keeps its pre-rendered HTML chips; this endpoint
    emits the same data as JSON so a separate frontend agent can
    render without re-parsing HTML.

    Returns the 50 most-recent instance rows for the app, joined
    against the events table's `wake.boot_started` rows for the
    per-row telemetry (Trigger, QueuedCount, ConcurrencyAtAdmit,
    AtCapacity, ReadyInMS). The aggregation math is shared with
    the HTML page:

      - 24h cutoff descending-break: the moment a row's
        `started_at` falls before the trailing-24h instant, the
        loop breaks (no further iteration). Pre-ADR-123 fleet
        rows with no `started_at` are not eligible for the break
        (always in scope).
      - Two-denominator rule for `at_capacity_pct`: numerator is
        the count of rows where the events join succeeded AND
        the at_capacity flag is true; denominator is the count
        of rows where the events join succeeded
        (`wake_count_with_meta`). Pre-PR-A fleet rows contribute
        to `wake_count_24h` but not the denominator — same
        posture as the HTML page.

    Plan-gated Hobby+ (mirror of /v1/apps/{slug}/metrics —
    same `code` so a downgrade between the two endpoints flips
    both at once).

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWakeTimelineResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> AppWakeTimelineResponse | Problem | None:
    """Per-app wake timeline (JSON mirror of the dashboard page).

     Wire-friendly mirror of the dashboard's per-app wake-timeline
    HTML page (`/dashboard/apps/{slug}/wake-timeline`,
    cmd/apid/handlers_dashboard.go:2548 renderAppWakeTimeline).
    The HTML page keeps its pre-rendered HTML chips; this endpoint
    emits the same data as JSON so a separate frontend agent can
    render without re-parsing HTML.

    Returns the 50 most-recent instance rows for the app, joined
    against the events table's `wake.boot_started` rows for the
    per-row telemetry (Trigger, QueuedCount, ConcurrencyAtAdmit,
    AtCapacity, ReadyInMS). The aggregation math is shared with
    the HTML page:

      - 24h cutoff descending-break: the moment a row's
        `started_at` falls before the trailing-24h instant, the
        loop breaks (no further iteration). Pre-ADR-123 fleet
        rows with no `started_at` are not eligible for the break
        (always in scope).
      - Two-denominator rule for `at_capacity_pct`: numerator is
        the count of rows where the events join succeeded AND
        the at_capacity flag is true; denominator is the count
        of rows where the events join succeeded
        (`wake_count_with_meta`). Pre-PR-A fleet rows contribute
        to `wake_count_24h` but not the denominator — same
        posture as the HTML page.

    Plan-gated Hobby+ (mirror of /v1/apps/{slug}/metrics —
    same `code` so a downgrade between the two endpoints flips
    both at once).

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWakeTimelineResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
        )
    ).parsed
