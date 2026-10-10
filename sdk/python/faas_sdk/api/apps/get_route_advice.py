import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_advice_response import RouteAdviceResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
    cache_max_age: int | Unset = 60,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["cache_max_age"] = cache_max_age

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/routes/advice".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteAdviceResponse | None:
    if response.status_code == 200:
        response_200 = RouteAdviceResponse.from_dict(response.json())

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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
) -> Response[Problem | RouteAdviceResponse]:
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
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
    cache_max_age: int | Unset = 60,
) -> Response[Problem | RouteAdviceResponse]:
    """Suggest edge rules for observed routes with what-if estimates.

     Read-only route advisor (ADR-940). Reads retained request telemetry for
    the app's busiest observed routes across deployments and suggests three
    kinds of edge rules: cache for anonymous, successful GET routes with
    repeat traffic; async for POST routes that time out or exceed a 10 s
    p95; and a per-consumer throttle when one consumer dominates a route and
    a limit exists that no other observed consumer reaches. A route needs at
    least 200 requests in the window, and routes that already have a rule of
    the suggested kind (enabled or not) are skipped.

    Each suggestion carries its evidence, an estimate replayed from the same
    window, cautions, and ready-to-create rule bodies: one per hostname (the
    platform hostname and every verified custom domain), always disabled.
    Nothing is applied; create the rules with POST /v1/apps/{slug}/edge-rules
    and enable them after review. Suggestion IDs are stable for the same
    kind, method and route. Estimates are observed_only and upper bounds:
    the cache estimate cannot see credential headers or query strings.
    Uses the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.

    Args:
        slug (str):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):
        cache_max_age (int | Unset):  Default: 60.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteAdviceResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        cache_max_age=cache_max_age,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
    cache_max_age: int | Unset = 60,
) -> Problem | RouteAdviceResponse | None:
    """Suggest edge rules for observed routes with what-if estimates.

     Read-only route advisor (ADR-940). Reads retained request telemetry for
    the app's busiest observed routes across deployments and suggests three
    kinds of edge rules: cache for anonymous, successful GET routes with
    repeat traffic; async for POST routes that time out or exceed a 10 s
    p95; and a per-consumer throttle when one consumer dominates a route and
    a limit exists that no other observed consumer reaches. A route needs at
    least 200 requests in the window, and routes that already have a rule of
    the suggested kind (enabled or not) are skipped.

    Each suggestion carries its evidence, an estimate replayed from the same
    window, cautions, and ready-to-create rule bodies: one per hostname (the
    platform hostname and every verified custom domain), always disabled.
    Nothing is applied; create the rules with POST /v1/apps/{slug}/edge-rules
    and enable them after review. Suggestion IDs are stable for the same
    kind, method and route. Estimates are observed_only and upper bounds:
    the cache estimate cannot see credential headers or query strings.
    Uses the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.

    Args:
        slug (str):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):
        cache_max_age (int | Unset):  Default: 60.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteAdviceResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
        cache_max_age=cache_max_age,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
    cache_max_age: int | Unset = 60,
) -> Response[Problem | RouteAdviceResponse]:
    """Suggest edge rules for observed routes with what-if estimates.

     Read-only route advisor (ADR-940). Reads retained request telemetry for
    the app's busiest observed routes across deployments and suggests three
    kinds of edge rules: cache for anonymous, successful GET routes with
    repeat traffic; async for POST routes that time out or exceed a 10 s
    p95; and a per-consumer throttle when one consumer dominates a route and
    a limit exists that no other observed consumer reaches. A route needs at
    least 200 requests in the window, and routes that already have a rule of
    the suggested kind (enabled or not) are skipped.

    Each suggestion carries its evidence, an estimate replayed from the same
    window, cautions, and ready-to-create rule bodies: one per hostname (the
    platform hostname and every verified custom domain), always disabled.
    Nothing is applied; create the rules with POST /v1/apps/{slug}/edge-rules
    and enable them after review. Suggestion IDs are stable for the same
    kind, method and route. Estimates are observed_only and upper bounds:
    the cache estimate cannot see credential headers or query strings.
    Uses the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.

    Args:
        slug (str):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):
        cache_max_age (int | Unset):  Default: 60.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteAdviceResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        cache_max_age=cache_max_age,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
    cache_max_age: int | Unset = 60,
) -> Problem | RouteAdviceResponse | None:
    """Suggest edge rules for observed routes with what-if estimates.

     Read-only route advisor (ADR-940). Reads retained request telemetry for
    the app's busiest observed routes across deployments and suggests three
    kinds of edge rules: cache for anonymous, successful GET routes with
    repeat traffic; async for POST routes that time out or exceed a 10 s
    p95; and a per-consumer throttle when one consumer dominates a route and
    a limit exists that no other observed consumer reaches. A route needs at
    least 200 requests in the window, and routes that already have a rule of
    the suggested kind (enabled or not) are skipped.

    Each suggestion carries its evidence, an estimate replayed from the same
    window, cautions, and ready-to-create rule bodies: one per hostname (the
    platform hostname and every verified custom domain), always disabled.
    Nothing is applied; create the rules with POST /v1/apps/{slug}/edge-rules
    and enable them after review. Suggestion IDs are stable for the same
    kind, method and route. Estimates are observed_only and upper bounds:
    the cache estimate cannot see credential headers or query strings.
    Uses the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.

    Args:
        slug (str):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):
        cache_max_age (int | Unset):  Default: 60.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteAdviceResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
            cache_max_age=cache_max_age,
        )
    ).parsed
