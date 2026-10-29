from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_insight_fix_request import RouteInsightFixRequest
from ...models.route_insight_fix_response import RouteInsightFixResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: RouteInsightFixRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/routes/insights/{id}/fix".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteInsightFixResponse | None:
    if response.status_code == 200:
        response_200 = RouteInsightFixResponse.from_dict(response.json())

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
) -> Response[Problem | RouteInsightFixResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RouteInsightFixRequest,
) -> Response[Problem | RouteInsightFixResponse]:
    """Apply a route insight's fix.

     Applies the edge-rule change the insight's `fix` describes: `create_cache_rule` creates an enabled
    `@app`
    cache rule with the measured safe max-age, `set_cache_max_age` and
    `disable_cache_rule` change the cache rule serving exactly this route,
    and `add_cache_override` adds a narrower rule ahead of a rule that also
    covers other routes. A `cache_stale` fix then purges the route's cached
    responses. Rules are written through the same path as the edge-rules
    API, so plan quotas, validation and fleet convergence apply.

    `fix_id` must be the `fix.id` the insight lists now; if the insight or
    the app's edge rules changed, the request fails with 409
    `route_insight_fix_changed` and nothing is written. An insight that is
    resolved or has no fix fails with 409 `route_insight_no_fix`. On
    success the route's affected insights resolve with `fix_action` and
    `fixed_at`, and stay resolved for at least one evidence window. Uses
    the deploy-write scopes, completed MFA, the DebugTelemetryEnabled plan
    gate and Idempotency-Key replay.

    Args:
        slug (str):
        id (UUID):
        body (RouteInsightFixRequest): Applies the fix an insight listed; refused when the fix
            changed since.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteInsightFixResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RouteInsightFixRequest,
) -> Problem | RouteInsightFixResponse | None:
    """Apply a route insight's fix.

     Applies the edge-rule change the insight's `fix` describes: `create_cache_rule` creates an enabled
    `@app`
    cache rule with the measured safe max-age, `set_cache_max_age` and
    `disable_cache_rule` change the cache rule serving exactly this route,
    and `add_cache_override` adds a narrower rule ahead of a rule that also
    covers other routes. A `cache_stale` fix then purges the route's cached
    responses. Rules are written through the same path as the edge-rules
    API, so plan quotas, validation and fleet convergence apply.

    `fix_id` must be the `fix.id` the insight lists now; if the insight or
    the app's edge rules changed, the request fails with 409
    `route_insight_fix_changed` and nothing is written. An insight that is
    resolved or has no fix fails with 409 `route_insight_no_fix`. On
    success the route's affected insights resolve with `fix_action` and
    `fixed_at`, and stay resolved for at least one evidence window. Uses
    the deploy-write scopes, completed MFA, the DebugTelemetryEnabled plan
    gate and Idempotency-Key replay.

    Args:
        slug (str):
        id (UUID):
        body (RouteInsightFixRequest): Applies the fix an insight listed; refused when the fix
            changed since.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteInsightFixResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RouteInsightFixRequest,
) -> Response[Problem | RouteInsightFixResponse]:
    """Apply a route insight's fix.

     Applies the edge-rule change the insight's `fix` describes: `create_cache_rule` creates an enabled
    `@app`
    cache rule with the measured safe max-age, `set_cache_max_age` and
    `disable_cache_rule` change the cache rule serving exactly this route,
    and `add_cache_override` adds a narrower rule ahead of a rule that also
    covers other routes. A `cache_stale` fix then purges the route's cached
    responses. Rules are written through the same path as the edge-rules
    API, so plan quotas, validation and fleet convergence apply.

    `fix_id` must be the `fix.id` the insight lists now; if the insight or
    the app's edge rules changed, the request fails with 409
    `route_insight_fix_changed` and nothing is written. An insight that is
    resolved or has no fix fails with 409 `route_insight_no_fix`. On
    success the route's affected insights resolve with `fix_action` and
    `fixed_at`, and stay resolved for at least one evidence window. Uses
    the deploy-write scopes, completed MFA, the DebugTelemetryEnabled plan
    gate and Idempotency-Key replay.

    Args:
        slug (str):
        id (UUID):
        body (RouteInsightFixRequest): Applies the fix an insight listed; refused when the fix
            changed since.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteInsightFixResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RouteInsightFixRequest,
) -> Problem | RouteInsightFixResponse | None:
    """Apply a route insight's fix.

     Applies the edge-rule change the insight's `fix` describes: `create_cache_rule` creates an enabled
    `@app`
    cache rule with the measured safe max-age, `set_cache_max_age` and
    `disable_cache_rule` change the cache rule serving exactly this route,
    and `add_cache_override` adds a narrower rule ahead of a rule that also
    covers other routes. A `cache_stale` fix then purges the route's cached
    responses. Rules are written through the same path as the edge-rules
    API, so plan quotas, validation and fleet convergence apply.

    `fix_id` must be the `fix.id` the insight lists now; if the insight or
    the app's edge rules changed, the request fails with 409
    `route_insight_fix_changed` and nothing is written. An insight that is
    resolved or has no fix fails with 409 `route_insight_no_fix`. On
    success the route's affected insights resolve with `fix_action` and
    `fixed_at`, and stay resolved for at least one evidence window. Uses
    the deploy-write scopes, completed MFA, the DebugTelemetryEnabled plan
    gate and Idempotency-Key replay.

    Args:
        slug (str):
        id (UUID):
        body (RouteInsightFixRequest): Applies the fix an insight listed; refused when the fix
            changed since.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteInsightFixResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
