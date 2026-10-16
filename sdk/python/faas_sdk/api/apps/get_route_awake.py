import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_route_awake_scope import GetRouteAwakeScope
from ...models.problem import Problem
from ...models.route_awake_response import RouteAwakeResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: GetRouteAwakeScope | Unset = UNSET,
    environment: str | Unset = UNSET,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_scope: str | Unset = UNSET
    if not isinstance(scope, Unset):
        json_scope = scope

    params["scope"] = json_scope

    params["environment"] = environment

    params["since"] = since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/routes/awake".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteAwakeResponse | None:
    if response.status_code == 200:
        response_200 = RouteAwakeResponse.from_dict(response.json())

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
) -> Response[Problem | RouteAwakeResponse]:
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
    scope: GetRouteAwakeScope | Unset = UNSET,
    environment: str | Unset = UNSET,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
) -> Response[Problem | RouteAwakeResponse]:
    """Show which routes keep a scale-to-zero app awake.

     Read-only awake-time report (ADR-1187). Awake time is the app's billed
    running time (plan RAM + 8 MB per running second) from the usage
    ledger. It is attributed, minute by minute, to the routes whose
    requests restarted the idle timer: a 2xx response from an instance, or
    a request that cold-booted it. Requests the gateway answered itself
    (edge cache hits, edge answers, rejected credentials) never wake the
    app and are not counted.

    For each of the busiest ten routes the report estimates the awake time
    and the change in cold wakes had that route's requests never reached
    the app, flags routes called at a steady interval (uptime monitors,
    pollers), and suggests serving anonymous GET traffic from the edge
    cache or calling less often than the idle timeout. Awake time no
    request explains (synthetic probes, scheduled prewarm, minimum
    instances, queue, cron and waitUntil work) is reported as
    unattributed. Minute resolution makes every figure an estimate. Uses
    the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.
    An explicit scope=default or environment selector returns partial,
    retained deployment-attributed history with immutable app/environment
    identities. Unattributable evidence is excluded, not counted as zero.
    Legacy body comparisons are excluded from scoped learned cacheability;
    only deployment-isolated observations qualify. Scoped advice omits
    app-wide setting suggestions and rule bodies; existing environment
    policies are not evaluated. Neither selector preserves the legacy
    aggregate report. Duplicate or mixed selectors are rejected.

    Args:
        slug (str):
        scope (GetRouteAwakeScope | Unset):
        environment (str | Unset):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteAwakeResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        environment=environment,
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
    scope: GetRouteAwakeScope | Unset = UNSET,
    environment: str | Unset = UNSET,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
) -> Problem | RouteAwakeResponse | None:
    """Show which routes keep a scale-to-zero app awake.

     Read-only awake-time report (ADR-1187). Awake time is the app's billed
    running time (plan RAM + 8 MB per running second) from the usage
    ledger. It is attributed, minute by minute, to the routes whose
    requests restarted the idle timer: a 2xx response from an instance, or
    a request that cold-booted it. Requests the gateway answered itself
    (edge cache hits, edge answers, rejected credentials) never wake the
    app and are not counted.

    For each of the busiest ten routes the report estimates the awake time
    and the change in cold wakes had that route's requests never reached
    the app, flags routes called at a steady interval (uptime monitors,
    pollers), and suggests serving anonymous GET traffic from the edge
    cache or calling less often than the idle timeout. Awake time no
    request explains (synthetic probes, scheduled prewarm, minimum
    instances, queue, cron and waitUntil work) is reported as
    unattributed. Minute resolution makes every figure an estimate. Uses
    the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.
    An explicit scope=default or environment selector returns partial,
    retained deployment-attributed history with immutable app/environment
    identities. Unattributable evidence is excluded, not counted as zero.
    Legacy body comparisons are excluded from scoped learned cacheability;
    only deployment-isolated observations qualify. Scoped advice omits
    app-wide setting suggestions and rule bodies; existing environment
    policies are not evaluated. Neither selector preserves the legacy
    aggregate report. Duplicate or mixed selectors are rejected.

    Args:
        slug (str):
        scope (GetRouteAwakeScope | Unset):
        environment (str | Unset):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteAwakeResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        environment=environment,
        since=since,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: GetRouteAwakeScope | Unset = UNSET,
    environment: str | Unset = UNSET,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
) -> Response[Problem | RouteAwakeResponse]:
    """Show which routes keep a scale-to-zero app awake.

     Read-only awake-time report (ADR-1187). Awake time is the app's billed
    running time (plan RAM + 8 MB per running second) from the usage
    ledger. It is attributed, minute by minute, to the routes whose
    requests restarted the idle timer: a 2xx response from an instance, or
    a request that cold-booted it. Requests the gateway answered itself
    (edge cache hits, edge answers, rejected credentials) never wake the
    app and are not counted.

    For each of the busiest ten routes the report estimates the awake time
    and the change in cold wakes had that route's requests never reached
    the app, flags routes called at a steady interval (uptime monitors,
    pollers), and suggests serving anonymous GET traffic from the edge
    cache or calling less often than the idle timeout. Awake time no
    request explains (synthetic probes, scheduled prewarm, minimum
    instances, queue, cron and waitUntil work) is reported as
    unattributed. Minute resolution makes every figure an estimate. Uses
    the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.
    An explicit scope=default or environment selector returns partial,
    retained deployment-attributed history with immutable app/environment
    identities. Unattributable evidence is excluded, not counted as zero.
    Legacy body comparisons are excluded from scoped learned cacheability;
    only deployment-isolated observations qualify. Scoped advice omits
    app-wide setting suggestions and rule bodies; existing environment
    policies are not evaluated. Neither selector preserves the legacy
    aggregate report. Duplicate or mixed selectors are rejected.

    Args:
        slug (str):
        scope (GetRouteAwakeScope | Unset):
        environment (str | Unset):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteAwakeResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        environment=environment,
        since=since,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: GetRouteAwakeScope | Unset = UNSET,
    environment: str | Unset = UNSET,
    since: str | Unset = "168h",
    until: datetime.datetime | Unset = UNSET,
) -> Problem | RouteAwakeResponse | None:
    """Show which routes keep a scale-to-zero app awake.

     Read-only awake-time report (ADR-1187). Awake time is the app's billed
    running time (plan RAM + 8 MB per running second) from the usage
    ledger. It is attributed, minute by minute, to the routes whose
    requests restarted the idle timer: a 2xx response from an instance, or
    a request that cold-booted it. Requests the gateway answered itself
    (edge cache hits, edge answers, rejected credentials) never wake the
    app and are not counted.

    For each of the busiest ten routes the report estimates the awake time
    and the change in cold wakes had that route's requests never reached
    the app, flags routes called at a steady interval (uptime monitors,
    pollers), and suggests serving anonymous GET traffic from the edge
    cache or calling less often than the idle timeout. Awake time no
    request explains (synthetic probes, scheduled prewarm, minimum
    instances, queue, cron and waitUntil work) is reported as
    unattributed. Minute resolution makes every figure an estimate. Uses
    the normal read scopes, completed MFA and the DebugTelemetryEnabled
    plan gate.
    An explicit scope=default or environment selector returns partial,
    retained deployment-attributed history with immutable app/environment
    identities. Unattributable evidence is excluded, not counted as zero.
    Legacy body comparisons are excluded from scoped learned cacheability;
    only deployment-isolated observations qualify. Scoped advice omits
    app-wide setting suggestions and rule bodies; existing environment
    policies are not evaluated. Neither selector preserves the legacy
    aggregate report. Duplicate or mixed selectors are rejected.

    Args:
        slug (str):
        scope (GetRouteAwakeScope | Unset):
        environment (str | Unset):
        since (str | Unset):  Default: '168h'.
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteAwakeResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            environment=environment,
            since=since,
            until=until,
        )
    ).parsed
