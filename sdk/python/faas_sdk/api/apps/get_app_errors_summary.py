import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_errors_summary_response import AppErrorsSummaryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: datetime.datetime | None | Unset = UNSET,
    until: datetime.datetime | None | Unset = UNSET,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: None | str | Unset
    if isinstance(since, Unset):
        json_since = UNSET
    elif isinstance(since, datetime.datetime):
        json_since = since.isoformat()
    else:
        json_since = since
    params["since"] = json_since

    json_until: None | str | Unset
    if isinstance(until, Unset):
        json_until = UNSET
    elif isinstance(until, datetime.datetime):
        json_until = until.isoformat()
    else:
        json_until = until
    params["until"] = json_until

    json_cursor: None | str | Unset
    if isinstance(cursor, Unset):
        json_cursor = UNSET
    else:
        json_cursor = cursor
    params["cursor"] = json_cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/errors/summary".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppErrorsSummaryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppErrorsSummaryResponse.from_dict(response.json())

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
) -> Response[AppErrorsSummaryResponse | Problem]:
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
    since: datetime.datetime | None | Unset = UNSET,
    until: datetime.datetime | None | Unset = UNSET,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[AppErrorsSummaryResponse | Problem]:
    r"""Per-app customer-facing automatic error grouping summary (ADR-096 / PR-B).

     Sentry-style grouped error view scoped to a customer's
    app. One row per `(account_id, app_id, fingerprint)` over
    the requested `[since, until]` window, sorted by `count
    DESC, last_seen_at DESC, fingerprint ASC`. Distinct from
    `GET /v1/apps/{slug}/slo` (issue #696 / ADR-082) which is
    the closed-set SLO summary (`1h` / `24h` / `7d`) — the
    errors summary uses a continuous `[since, until]` window
    with an explicit RFC3339Nano stamp instead.

    The window is clamped to `AppErrorsWindowMaxHours` (168h).
    When the clamp fires, `window_clamped` is true so the
    dashboard can render a \"you widened the window past the
    cap\" tile. The endpoint returns 200 with `items: []`
    when no fingerprints are present in the window — never
    404. Cross-account slug is a 404 (IDOR-safe; the error
    is byte-identical to a real \"no such app\" 404).

    Fingerprints are derived at write time as
    `sha256(route_template || \"\x1f\" || http_status ||
    \"\x1f\" || error_class)`. The route is the matched
    template (e.g. `/users/{id}`), NEVER the expanded URL —
    this is the load-bearing cardinality fix that keeps the
    top-N bounded.

    Args:
        slug (str):
        since (datetime.datetime | None | Unset):
        until (datetime.datetime | None | Unset):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppErrorsSummaryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | None | Unset = UNSET,
    until: datetime.datetime | None | Unset = UNSET,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> AppErrorsSummaryResponse | Problem | None:
    r"""Per-app customer-facing automatic error grouping summary (ADR-096 / PR-B).

     Sentry-style grouped error view scoped to a customer's
    app. One row per `(account_id, app_id, fingerprint)` over
    the requested `[since, until]` window, sorted by `count
    DESC, last_seen_at DESC, fingerprint ASC`. Distinct from
    `GET /v1/apps/{slug}/slo` (issue #696 / ADR-082) which is
    the closed-set SLO summary (`1h` / `24h` / `7d`) — the
    errors summary uses a continuous `[since, until]` window
    with an explicit RFC3339Nano stamp instead.

    The window is clamped to `AppErrorsWindowMaxHours` (168h).
    When the clamp fires, `window_clamped` is true so the
    dashboard can render a \"you widened the window past the
    cap\" tile. The endpoint returns 200 with `items: []`
    when no fingerprints are present in the window — never
    404. Cross-account slug is a 404 (IDOR-safe; the error
    is byte-identical to a real \"no such app\" 404).

    Fingerprints are derived at write time as
    `sha256(route_template || \"\x1f\" || http_status ||
    \"\x1f\" || error_class)`. The route is the matched
    template (e.g. `/users/{id}`), NEVER the expanded URL —
    this is the load-bearing cardinality fix that keeps the
    top-N bounded.

    Args:
        slug (str):
        since (datetime.datetime | None | Unset):
        until (datetime.datetime | None | Unset):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppErrorsSummaryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | None | Unset = UNSET,
    until: datetime.datetime | None | Unset = UNSET,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[AppErrorsSummaryResponse | Problem]:
    r"""Per-app customer-facing automatic error grouping summary (ADR-096 / PR-B).

     Sentry-style grouped error view scoped to a customer's
    app. One row per `(account_id, app_id, fingerprint)` over
    the requested `[since, until]` window, sorted by `count
    DESC, last_seen_at DESC, fingerprint ASC`. Distinct from
    `GET /v1/apps/{slug}/slo` (issue #696 / ADR-082) which is
    the closed-set SLO summary (`1h` / `24h` / `7d`) — the
    errors summary uses a continuous `[since, until]` window
    with an explicit RFC3339Nano stamp instead.

    The window is clamped to `AppErrorsWindowMaxHours` (168h).
    When the clamp fires, `window_clamped` is true so the
    dashboard can render a \"you widened the window past the
    cap\" tile. The endpoint returns 200 with `items: []`
    when no fingerprints are present in the window — never
    404. Cross-account slug is a 404 (IDOR-safe; the error
    is byte-identical to a real \"no such app\" 404).

    Fingerprints are derived at write time as
    `sha256(route_template || \"\x1f\" || http_status ||
    \"\x1f\" || error_class)`. The route is the matched
    template (e.g. `/users/{id}`), NEVER the expanded URL —
    this is the load-bearing cardinality fix that keeps the
    top-N bounded.

    Args:
        slug (str):
        since (datetime.datetime | None | Unset):
        until (datetime.datetime | None | Unset):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppErrorsSummaryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | None | Unset = UNSET,
    until: datetime.datetime | None | Unset = UNSET,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> AppErrorsSummaryResponse | Problem | None:
    r"""Per-app customer-facing automatic error grouping summary (ADR-096 / PR-B).

     Sentry-style grouped error view scoped to a customer's
    app. One row per `(account_id, app_id, fingerprint)` over
    the requested `[since, until]` window, sorted by `count
    DESC, last_seen_at DESC, fingerprint ASC`. Distinct from
    `GET /v1/apps/{slug}/slo` (issue #696 / ADR-082) which is
    the closed-set SLO summary (`1h` / `24h` / `7d`) — the
    errors summary uses a continuous `[since, until]` window
    with an explicit RFC3339Nano stamp instead.

    The window is clamped to `AppErrorsWindowMaxHours` (168h).
    When the clamp fires, `window_clamped` is true so the
    dashboard can render a \"you widened the window past the
    cap\" tile. The endpoint returns 200 with `items: []`
    when no fingerprints are present in the window — never
    404. Cross-account slug is a 404 (IDOR-safe; the error
    is byte-identical to a real \"no such app\" 404).

    Fingerprints are derived at write time as
    `sha256(route_template || \"\x1f\" || http_status ||
    \"\x1f\" || error_class)`. The route is the matched
    template (e.g. `/users/{id}`), NEVER the expanded URL —
    this is the load-bearing cardinality fix that keeps the
    top-N bounded.

    Args:
        slug (str):
        since (datetime.datetime | None | Unset):
        until (datetime.datetime | None | Unset):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppErrorsSummaryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
