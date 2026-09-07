from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_telemetry_list_response import DebugTelemetryListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
    route: None | str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: None | str | Unset
    if isinstance(since, Unset):
        json_since = UNSET
    else:
        json_since = since
    params["since"] = json_since

    json_limit: int | None | Unset
    if isinstance(limit, Unset):
        json_limit = UNSET
    else:
        json_limit = limit
    params["limit"] = json_limit

    json_route: None | str | Unset
    if isinstance(route, Unset):
        json_route = UNSET
    else:
        json_route = route
    params["route"] = json_route

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/debug/requests".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugTelemetryListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DebugTelemetryListResponse.from_dict(response.json())

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
) -> Response[DebugTelemetryListResponse | Problem]:
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
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
    route: None | str | Unset = UNSET,
) -> Response[DebugTelemetryListResponse | Problem]:
    r"""Per-app request telemetry (ADR-127 / PR-A).

     Recent request rows for an app — status, latency_ms, route,
    method, deployment_id, cold_boot, trace_id, received_at.
    PR-A ships the read endpoint only; the write-side (publisher
    → gRPC IncrementRequestTelemetry → apid receiver → sqlc
    INSERT) lands in PR-B. The endpoint is plan-gated by
    `DebugTelemetryEnabled` (Free off; Hobby/Pro/Scale on).
    The window is clamped to `DebugTelemetryRetentionDays`
    (Hobby 3d, Pro 7d, Scale 14d). When the clamp fires, the
    effective `since` is returned in the response so the
    dashboard can render a \"you widened past the cap\" tile.
    Returns 200 with `requests: []` when no rows exist in the
    window — never 404. Cross-account slug is 404 (IDOR-safe;
    byte-identical to \"no such app\").

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.
        route (None | str | Unset):  Exact route-template filter.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugTelemetryListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        limit=limit,
        route=route,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
    route: None | str | Unset = UNSET,
) -> DebugTelemetryListResponse | Problem | None:
    r"""Per-app request telemetry (ADR-127 / PR-A).

     Recent request rows for an app — status, latency_ms, route,
    method, deployment_id, cold_boot, trace_id, received_at.
    PR-A ships the read endpoint only; the write-side (publisher
    → gRPC IncrementRequestTelemetry → apid receiver → sqlc
    INSERT) lands in PR-B. The endpoint is plan-gated by
    `DebugTelemetryEnabled` (Free off; Hobby/Pro/Scale on).
    The window is clamped to `DebugTelemetryRetentionDays`
    (Hobby 3d, Pro 7d, Scale 14d). When the clamp fires, the
    effective `since` is returned in the response so the
    dashboard can render a \"you widened past the cap\" tile.
    Returns 200 with `requests: []` when no rows exist in the
    window — never 404. Cross-account slug is 404 (IDOR-safe;
    byte-identical to \"no such app\").

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.
        route (None | str | Unset):  Exact route-template filter.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugTelemetryListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        limit=limit,
        route=route,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
    route: None | str | Unset = UNSET,
) -> Response[DebugTelemetryListResponse | Problem]:
    r"""Per-app request telemetry (ADR-127 / PR-A).

     Recent request rows for an app — status, latency_ms, route,
    method, deployment_id, cold_boot, trace_id, received_at.
    PR-A ships the read endpoint only; the write-side (publisher
    → gRPC IncrementRequestTelemetry → apid receiver → sqlc
    INSERT) lands in PR-B. The endpoint is plan-gated by
    `DebugTelemetryEnabled` (Free off; Hobby/Pro/Scale on).
    The window is clamped to `DebugTelemetryRetentionDays`
    (Hobby 3d, Pro 7d, Scale 14d). When the clamp fires, the
    effective `since` is returned in the response so the
    dashboard can render a \"you widened past the cap\" tile.
    Returns 200 with `requests: []` when no rows exist in the
    window — never 404. Cross-account slug is 404 (IDOR-safe;
    byte-identical to \"no such app\").

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.
        route (None | str | Unset):  Exact route-template filter.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugTelemetryListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        limit=limit,
        route=route,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
    route: None | str | Unset = UNSET,
) -> DebugTelemetryListResponse | Problem | None:
    r"""Per-app request telemetry (ADR-127 / PR-A).

     Recent request rows for an app — status, latency_ms, route,
    method, deployment_id, cold_boot, trace_id, received_at.
    PR-A ships the read endpoint only; the write-side (publisher
    → gRPC IncrementRequestTelemetry → apid receiver → sqlc
    INSERT) lands in PR-B. The endpoint is plan-gated by
    `DebugTelemetryEnabled` (Free off; Hobby/Pro/Scale on).
    The window is clamped to `DebugTelemetryRetentionDays`
    (Hobby 3d, Pro 7d, Scale 14d). When the clamp fires, the
    effective `since` is returned in the response so the
    dashboard can render a \"you widened past the cap\" tile.
    Returns 200 with `requests: []` when no rows exist in the
    window — never 404. Cross-account slug is 404 (IDOR-safe;
    byte-identical to \"no such app\").

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.
        route (None | str | Unset):  Exact route-template filter.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugTelemetryListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            limit=limit,
            route=route,
        )
    ).parsed
