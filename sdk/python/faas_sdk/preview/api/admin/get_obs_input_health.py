from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_obs_input_health_expected_role import (
    GetObsInputHealthExpectedRole,
)
from ...models.obs_input_health import ObsInputHealth
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    expected_role: GetObsInputHealthExpectedRole | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_expected_role: str | Unset = UNSET
    if not isinstance(expected_role, Unset):
        json_expected_role = expected_role

    params["expected_role"] = json_expected_role

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/input-health",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsInputHealth | Problem | None:
    if response.status_code == 200:
        response_200 = ObsInputHealth.from_dict(response.json())

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
) -> Response[Any | ObsInputHealth | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_role: GetObsInputHealthExpectedRole | Unset = UNSET,
) -> Response[Any | ObsInputHealth | Problem]:
    """Inspect critical telemetry inputs for an explicitly selected role.

     Requires operator read scope, MFA and the operator allowlist. Without an
    explicit expected_role, returns unknown with choices and reads no source.
    Both roles expect at least one API, the gateway (public gateway) and the scheduler
    scrape target. Single-box also expects the VM manager. Control-plane adds the VM manager,
    the gateway and the compute scheduler counts from fresh the API discovery.
    Static counts are minimums, not a complete intended inventory. Compute
    counts do not prove target identity membership. The default Prometheus
    collision mapping job=the API plus exported_job selects discovery evidence.
    Missing, stale, conflicting or invalid discovery stays unknown. A fresh
    zero registry is legitimate; omitted invalid discovery targets degrade.
    Pins strict Prometheus queries to one time under a three-second budget
    with at most six concurrent fixed job readers. Compares original sample
    timestamps, distinguishing fresh up, down, stale and missing minimum
    targets, with unexpected compute counts reported separately. Checks one
    vetted always-instantiated daemon readiness gauge per job only on fresh
    up targets, preserving per-target gaps. Uses a ten-minute lookback at
    fifteen-second steps; freshness is 120 seconds. Gauge value zero is a
    valid input and does not imply ready. Other metrics and rule evaluation
    or notification health are unassessed. A known zero expectation with no
    observed targets makes metric checks not applicable. Failures remain
    unknown without cached values. Returns aggregate counts and closed codes,
    excluding target addresses, labels, queries, URLs and raw errors. No writes.

    Args:
        expected_role (GetObsInputHealthExpectedRole | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsInputHealth | Problem]
    """

    kwargs = _get_kwargs(
        expected_role=expected_role,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    expected_role: GetObsInputHealthExpectedRole | Unset = UNSET,
) -> Any | ObsInputHealth | Problem | None:
    """Inspect critical telemetry inputs for an explicitly selected role.

     Requires operator read scope, MFA and the operator allowlist. Without an
    explicit expected_role, returns unknown with choices and reads no source.
    Both roles expect at least one API, the gateway (public gateway) and the scheduler
    scrape target. Single-box also expects the VM manager. Control-plane adds the VM manager,
    the gateway and the compute scheduler counts from fresh the API discovery.
    Static counts are minimums, not a complete intended inventory. Compute
    counts do not prove target identity membership. The default Prometheus
    collision mapping job=the API plus exported_job selects discovery evidence.
    Missing, stale, conflicting or invalid discovery stays unknown. A fresh
    zero registry is legitimate; omitted invalid discovery targets degrade.
    Pins strict Prometheus queries to one time under a three-second budget
    with at most six concurrent fixed job readers. Compares original sample
    timestamps, distinguishing fresh up, down, stale and missing minimum
    targets, with unexpected compute counts reported separately. Checks one
    vetted always-instantiated daemon readiness gauge per job only on fresh
    up targets, preserving per-target gaps. Uses a ten-minute lookback at
    fifteen-second steps; freshness is 120 seconds. Gauge value zero is a
    valid input and does not imply ready. Other metrics and rule evaluation
    or notification health are unassessed. A known zero expectation with no
    observed targets makes metric checks not applicable. Failures remain
    unknown without cached values. Returns aggregate counts and closed codes,
    excluding target addresses, labels, queries, URLs and raw errors. No writes.

    Args:
        expected_role (GetObsInputHealthExpectedRole | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsInputHealth | Problem
    """

    return sync_detailed(
        client=client,
        expected_role=expected_role,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_role: GetObsInputHealthExpectedRole | Unset = UNSET,
) -> Response[Any | ObsInputHealth | Problem]:
    """Inspect critical telemetry inputs for an explicitly selected role.

     Requires operator read scope, MFA and the operator allowlist. Without an
    explicit expected_role, returns unknown with choices and reads no source.
    Both roles expect at least one API, the gateway (public gateway) and the scheduler
    scrape target. Single-box also expects the VM manager. Control-plane adds the VM manager,
    the gateway and the compute scheduler counts from fresh the API discovery.
    Static counts are minimums, not a complete intended inventory. Compute
    counts do not prove target identity membership. The default Prometheus
    collision mapping job=the API plus exported_job selects discovery evidence.
    Missing, stale, conflicting or invalid discovery stays unknown. A fresh
    zero registry is legitimate; omitted invalid discovery targets degrade.
    Pins strict Prometheus queries to one time under a three-second budget
    with at most six concurrent fixed job readers. Compares original sample
    timestamps, distinguishing fresh up, down, stale and missing minimum
    targets, with unexpected compute counts reported separately. Checks one
    vetted always-instantiated daemon readiness gauge per job only on fresh
    up targets, preserving per-target gaps. Uses a ten-minute lookback at
    fifteen-second steps; freshness is 120 seconds. Gauge value zero is a
    valid input and does not imply ready. Other metrics and rule evaluation
    or notification health are unassessed. A known zero expectation with no
    observed targets makes metric checks not applicable. Failures remain
    unknown without cached values. Returns aggregate counts and closed codes,
    excluding target addresses, labels, queries, URLs and raw errors. No writes.

    Args:
        expected_role (GetObsInputHealthExpectedRole | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsInputHealth | Problem]
    """

    kwargs = _get_kwargs(
        expected_role=expected_role,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    expected_role: GetObsInputHealthExpectedRole | Unset = UNSET,
) -> Any | ObsInputHealth | Problem | None:
    """Inspect critical telemetry inputs for an explicitly selected role.

     Requires operator read scope, MFA and the operator allowlist. Without an
    explicit expected_role, returns unknown with choices and reads no source.
    Both roles expect at least one API, the gateway (public gateway) and the scheduler
    scrape target. Single-box also expects the VM manager. Control-plane adds the VM manager,
    the gateway and the compute scheduler counts from fresh the API discovery.
    Static counts are minimums, not a complete intended inventory. Compute
    counts do not prove target identity membership. The default Prometheus
    collision mapping job=the API plus exported_job selects discovery evidence.
    Missing, stale, conflicting or invalid discovery stays unknown. A fresh
    zero registry is legitimate; omitted invalid discovery targets degrade.
    Pins strict Prometheus queries to one time under a three-second budget
    with at most six concurrent fixed job readers. Compares original sample
    timestamps, distinguishing fresh up, down, stale and missing minimum
    targets, with unexpected compute counts reported separately. Checks one
    vetted always-instantiated daemon readiness gauge per job only on fresh
    up targets, preserving per-target gaps. Uses a ten-minute lookback at
    fifteen-second steps; freshness is 120 seconds. Gauge value zero is a
    valid input and does not imply ready. Other metrics and rule evaluation
    or notification health are unassessed. A known zero expectation with no
    observed targets makes metric checks not applicable. Failures remain
    unknown without cached values. Returns aggregate counts and closed codes,
    excluding target addresses, labels, queries, URLs and raw errors. No writes.

    Args:
        expected_role (GetObsInputHealthExpectedRole | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsInputHealth | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            expected_role=expected_role,
        )
    ).parsed
