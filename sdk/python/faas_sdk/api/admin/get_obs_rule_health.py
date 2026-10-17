from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_rule_health_response import ObsRuleHealthResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/rule-health",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsRuleHealthResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ObsRuleHealthResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObsRuleHealthResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsRuleHealthResponse | Problem]:
    """Diagnose current alert-rule evaluation health.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Assesses the complete bounded Prometheus alert-rule
    snapshot under a three-second deadline and returns at most 200 metadata
    items, unhealthy first. Totals cover all loaded rules before preview
    truncation. An empty loaded-rule snapshot is unavailable, never healthy.
    Failed, stale, unavailable, clock-invalid and structurally invalid rules
    remain explicit; pending/firing counts are null for unhealthy rules.
    Reported state is the last reported rule state, not proof of recovery.
    Group names and file basenames are bounded safe text; absolute paths,
    queries, raw evaluation errors, labels and annotations are omitted.
    Invalid identity text is omitted and opaque hashes remain available.
    No collection, history or triage writes occur. This current read does
    not establish the cause of a past collection failure or completeness
    of an expected deployed rule inventory. Source errors return HTTP 200
    with unavailable state and null totals. Disabled configuration is explicit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRuleHealthResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsRuleHealthResponse | Problem | None:
    """Diagnose current alert-rule evaluation health.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Assesses the complete bounded Prometheus alert-rule
    snapshot under a three-second deadline and returns at most 200 metadata
    items, unhealthy first. Totals cover all loaded rules before preview
    truncation. An empty loaded-rule snapshot is unavailable, never healthy.
    Failed, stale, unavailable, clock-invalid and structurally invalid rules
    remain explicit; pending/firing counts are null for unhealthy rules.
    Reported state is the last reported rule state, not proof of recovery.
    Group names and file basenames are bounded safe text; absolute paths,
    queries, raw evaluation errors, labels and annotations are omitted.
    Invalid identity text is omitted and opaque hashes remain available.
    No collection, history or triage writes occur. This current read does
    not establish the cause of a past collection failure or completeness
    of an expected deployed rule inventory. Source errors return HTTP 200
    with unavailable state and null totals. Disabled configuration is explicit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRuleHealthResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsRuleHealthResponse | Problem]:
    """Diagnose current alert-rule evaluation health.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Assesses the complete bounded Prometheus alert-rule
    snapshot under a three-second deadline and returns at most 200 metadata
    items, unhealthy first. Totals cover all loaded rules before preview
    truncation. An empty loaded-rule snapshot is unavailable, never healthy.
    Failed, stale, unavailable, clock-invalid and structurally invalid rules
    remain explicit; pending/firing counts are null for unhealthy rules.
    Reported state is the last reported rule state, not proof of recovery.
    Group names and file basenames are bounded safe text; absolute paths,
    queries, raw evaluation errors, labels and annotations are omitted.
    Invalid identity text is omitted and opaque hashes remain available.
    No collection, history or triage writes occur. This current read does
    not establish the cause of a past collection failure or completeness
    of an expected deployed rule inventory. Source errors return HTTP 200
    with unavailable state and null totals. Disabled configuration is explicit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRuleHealthResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsRuleHealthResponse | Problem | None:
    """Diagnose current alert-rule evaluation health.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Assesses the complete bounded Prometheus alert-rule
    snapshot under a three-second deadline and returns at most 200 metadata
    items, unhealthy first. Totals cover all loaded rules before preview
    truncation. An empty loaded-rule snapshot is unavailable, never healthy.
    Failed, stale, unavailable, clock-invalid and structurally invalid rules
    remain explicit; pending/firing counts are null for unhealthy rules.
    Reported state is the last reported rule state, not proof of recovery.
    Group names and file basenames are bounded safe text; absolute paths,
    queries, raw evaluation errors, labels and annotations are omitted.
    Invalid identity text is omitted and opaque hashes remain available.
    No collection, history or triage writes occur. This current read does
    not establish the cause of a past collection failure or completeness
    of an expected deployed rule inventory. Source errors return HTTP 200
    with unavailable state and null totals. Disabled configuration is explicit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRuleHealthResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
