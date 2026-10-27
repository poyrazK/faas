import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_usage_summary_response import AppUsageSummaryResponse
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
        "url": "/v1/apps/{slug}/usage".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppUsageSummaryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppUsageSummaryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[AppUsageSummaryResponse | Problem]:
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
) -> Response[AppUsageSummaryResponse | Problem]:
    """Per-app billing usage summary (trailing 30d by default).

     Customer-facing billing rollup for one app over a caller-
    supplied window (default: trailing 30d, clamped at 90d upper
    bound). Plan-gated Hobby+ — Free gets 402
    `plan_app_usage_summary_not_allowed`.

    Window resolution: `since` and `until` are RFC3339 timestamps.
    Both default to UTC midnight snaps; `until` defaults to
    `now` snapped down, `since` defaults to `until - 30d`. The
    handler clamps `since` to `until - 90d` so a customer cannot
    unbounded-scan `usage_minutes` (retention is 30d; the
    90d ceiling is a forward-compatibility ceiling for when
    `usage_daily` lands).

    Overage computation: `overage_gb_hours = max(0, gb_hours -
    plan_included_gb_hours)`. The included band is echoed from
    `acct.Plan.PlanIncludedGBHours`; the overage figure is the
    integer-rounded float the Stripe pusher bills at $0.01/GB-h.

    Source: `usage_minutes` today (after the 30d retention cap).
    `usage_daily` / `mixed` land with the trail-period reader
    follow-up — same wire shape, no migration needed.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppUsageSummaryResponse | Problem]
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
) -> AppUsageSummaryResponse | Problem | None:
    """Per-app billing usage summary (trailing 30d by default).

     Customer-facing billing rollup for one app over a caller-
    supplied window (default: trailing 30d, clamped at 90d upper
    bound). Plan-gated Hobby+ — Free gets 402
    `plan_app_usage_summary_not_allowed`.

    Window resolution: `since` and `until` are RFC3339 timestamps.
    Both default to UTC midnight snaps; `until` defaults to
    `now` snapped down, `since` defaults to `until - 30d`. The
    handler clamps `since` to `until - 90d` so a customer cannot
    unbounded-scan `usage_minutes` (retention is 30d; the
    90d ceiling is a forward-compatibility ceiling for when
    `usage_daily` lands).

    Overage computation: `overage_gb_hours = max(0, gb_hours -
    plan_included_gb_hours)`. The included band is echoed from
    `acct.Plan.PlanIncludedGBHours`; the overage figure is the
    integer-rounded float the Stripe pusher bills at $0.01/GB-h.

    Source: `usage_minutes` today (after the 30d retention cap).
    `usage_daily` / `mixed` land with the trail-period reader
    follow-up — same wire shape, no migration needed.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppUsageSummaryResponse | Problem
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
) -> Response[AppUsageSummaryResponse | Problem]:
    """Per-app billing usage summary (trailing 30d by default).

     Customer-facing billing rollup for one app over a caller-
    supplied window (default: trailing 30d, clamped at 90d upper
    bound). Plan-gated Hobby+ — Free gets 402
    `plan_app_usage_summary_not_allowed`.

    Window resolution: `since` and `until` are RFC3339 timestamps.
    Both default to UTC midnight snaps; `until` defaults to
    `now` snapped down, `since` defaults to `until - 30d`. The
    handler clamps `since` to `until - 90d` so a customer cannot
    unbounded-scan `usage_minutes` (retention is 30d; the
    90d ceiling is a forward-compatibility ceiling for when
    `usage_daily` lands).

    Overage computation: `overage_gb_hours = max(0, gb_hours -
    plan_included_gb_hours)`. The included band is echoed from
    `acct.Plan.PlanIncludedGBHours`; the overage figure is the
    integer-rounded float the Stripe pusher bills at $0.01/GB-h.

    Source: `usage_minutes` today (after the 30d retention cap).
    `usage_daily` / `mixed` land with the trail-period reader
    follow-up — same wire shape, no migration needed.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppUsageSummaryResponse | Problem]
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
) -> AppUsageSummaryResponse | Problem | None:
    """Per-app billing usage summary (trailing 30d by default).

     Customer-facing billing rollup for one app over a caller-
    supplied window (default: trailing 30d, clamped at 90d upper
    bound). Plan-gated Hobby+ — Free gets 402
    `plan_app_usage_summary_not_allowed`.

    Window resolution: `since` and `until` are RFC3339 timestamps.
    Both default to UTC midnight snaps; `until` defaults to
    `now` snapped down, `since` defaults to `until - 30d`. The
    handler clamps `since` to `until - 90d` so a customer cannot
    unbounded-scan `usage_minutes` (retention is 30d; the
    90d ceiling is a forward-compatibility ceiling for when
    `usage_daily` lands).

    Overage computation: `overage_gb_hours = max(0, gb_hours -
    plan_included_gb_hours)`. The included band is echoed from
    `acct.Plan.PlanIncludedGBHours`; the overage figure is the
    integer-rounded float the Stripe pusher bills at $0.01/GB-h.

    Source: `usage_minutes` today (after the 30d retention cap).
    `usage_daily` / `mixed` land with the trail-period reader
    follow-up — same wire shape, no migration needed.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppUsageSummaryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
        )
    ).parsed
