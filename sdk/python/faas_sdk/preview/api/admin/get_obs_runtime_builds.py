import datetime
from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_obs_runtime_builds_expected_role import (
    GetObsRuntimeBuildsExpectedRole,
)
from ...models.obs_runtime_builds import ObsRuntimeBuilds
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    release: str,
    after: datetime.datetime,
    expected_role: GetObsRuntimeBuildsExpectedRole | Unset = UNSET,
    compare_at: datetime.datetime | Unset = UNSET,
    diagnostics: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["release"] = release

    json_after = after.isoformat()
    params["after"] = json_after

    json_expected_role: str | Unset = UNSET
    if not isinstance(expected_role, Unset):
        json_expected_role = expected_role

    params["expected_role"] = json_expected_role

    json_compare_at: str | Unset = UNSET
    if not isinstance(compare_at, Unset):
        json_compare_at = compare_at.isoformat()
    params["compare_at"] = json_compare_at

    params["diagnostics"] = diagnostics

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/runtime-builds",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsRuntimeBuilds | Problem | None:
    if response.status_code == 200:
        response_200 = ObsRuntimeBuilds.from_dict(response.json())

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
) -> Response[Any | ObsRuntimeBuilds | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    release: str,
    after: datetime.datetime,
    expected_role: GetObsRuntimeBuildsExpectedRole | Unset = UNSET,
    compare_at: datetime.datetime | Unset = UNSET,
    diagnostics: str | Unset = UNSET,
) -> Response[Any | ObsRuntimeBuilds | Problem]:
    """Inspect advisory running-build evidence for selected critical daemons.

     Requires operator read scope, MFA and the operator allowlist. Read-only,
    advisory identity evidence; this does not qualify or attest a deployment.
    Select a versioned build profile explicitly. V1 profiles single-box-v1
    and control-plane-v1 preserve the original critical jobs. V2 profiles
    single-box-v2 and control-plane-v2 additionally inspect build and
    image workers. Single-box expects their static scrape targets; control-plane
    expects them on every active configured compute-registry member, using
    the existing discovery scope. Telemetry input-health roles stay v1.
    Without a role, no sources are read and the state is unknown. Selected
    build jobs do not cover every daemon in the fleet. Static
    jobs use scrape target membership and a minimum of one. Compute jobs
    compare exact node and node_id labels to the active configured compute
    registry, so replacement nodes cannot hide missing members behind equal
    counts. Draining inactive and unconfigured nodes are outside this scope.
    Strict queries pin scrape and build-info values and original sample
    timestamps to one evaluation time within three seconds, using at most eight
    fixed job readers. Down, missing,
    stale, future, pre-boundary, conflicting and unknown-revision evidence
    never counts as matching. Freshness is 120 seconds, with 30 seconds of
    clock skew at the boundary. Only complete lowercase 40-character commit
    SHAs are compared. Target addresses and raw labels, build-time strings
    and raw provider errors are excluded; target_id is a SHA-256 digest.
    Counts cover all inspected evidence, even beyond the 32-item preview.
    Mixed or mismatched revisions can coexist with incomplete inventory;
    inspect job availability and all counts. No cached success is returned.

    Args:
        release (str):
        after (datetime.datetime):
        expected_role (GetObsRuntimeBuildsExpectedRole | Unset):
        compare_at (datetime.datetime | Unset):
        diagnostics (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRuntimeBuilds | Problem]
    """

    kwargs = _get_kwargs(
        release=release,
        after=after,
        expected_role=expected_role,
        compare_at=compare_at,
        diagnostics=diagnostics,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    release: str,
    after: datetime.datetime,
    expected_role: GetObsRuntimeBuildsExpectedRole | Unset = UNSET,
    compare_at: datetime.datetime | Unset = UNSET,
    diagnostics: str | Unset = UNSET,
) -> Any | ObsRuntimeBuilds | Problem | None:
    """Inspect advisory running-build evidence for selected critical daemons.

     Requires operator read scope, MFA and the operator allowlist. Read-only,
    advisory identity evidence; this does not qualify or attest a deployment.
    Select a versioned build profile explicitly. V1 profiles single-box-v1
    and control-plane-v1 preserve the original critical jobs. V2 profiles
    single-box-v2 and control-plane-v2 additionally inspect build and
    image workers. Single-box expects their static scrape targets; control-plane
    expects them on every active configured compute-registry member, using
    the existing discovery scope. Telemetry input-health roles stay v1.
    Without a role, no sources are read and the state is unknown. Selected
    build jobs do not cover every daemon in the fleet. Static
    jobs use scrape target membership and a minimum of one. Compute jobs
    compare exact node and node_id labels to the active configured compute
    registry, so replacement nodes cannot hide missing members behind equal
    counts. Draining inactive and unconfigured nodes are outside this scope.
    Strict queries pin scrape and build-info values and original sample
    timestamps to one evaluation time within three seconds, using at most eight
    fixed job readers. Down, missing,
    stale, future, pre-boundary, conflicting and unknown-revision evidence
    never counts as matching. Freshness is 120 seconds, with 30 seconds of
    clock skew at the boundary. Only complete lowercase 40-character commit
    SHAs are compared. Target addresses and raw labels, build-time strings
    and raw provider errors are excluded; target_id is a SHA-256 digest.
    Counts cover all inspected evidence, even beyond the 32-item preview.
    Mixed or mismatched revisions can coexist with incomplete inventory;
    inspect job availability and all counts. No cached success is returned.

    Args:
        release (str):
        after (datetime.datetime):
        expected_role (GetObsRuntimeBuildsExpectedRole | Unset):
        compare_at (datetime.datetime | Unset):
        diagnostics (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRuntimeBuilds | Problem
    """

    return sync_detailed(
        client=client,
        release=release,
        after=after,
        expected_role=expected_role,
        compare_at=compare_at,
        diagnostics=diagnostics,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    release: str,
    after: datetime.datetime,
    expected_role: GetObsRuntimeBuildsExpectedRole | Unset = UNSET,
    compare_at: datetime.datetime | Unset = UNSET,
    diagnostics: str | Unset = UNSET,
) -> Response[Any | ObsRuntimeBuilds | Problem]:
    """Inspect advisory running-build evidence for selected critical daemons.

     Requires operator read scope, MFA and the operator allowlist. Read-only,
    advisory identity evidence; this does not qualify or attest a deployment.
    Select a versioned build profile explicitly. V1 profiles single-box-v1
    and control-plane-v1 preserve the original critical jobs. V2 profiles
    single-box-v2 and control-plane-v2 additionally inspect build and
    image workers. Single-box expects their static scrape targets; control-plane
    expects them on every active configured compute-registry member, using
    the existing discovery scope. Telemetry input-health roles stay v1.
    Without a role, no sources are read and the state is unknown. Selected
    build jobs do not cover every daemon in the fleet. Static
    jobs use scrape target membership and a minimum of one. Compute jobs
    compare exact node and node_id labels to the active configured compute
    registry, so replacement nodes cannot hide missing members behind equal
    counts. Draining inactive and unconfigured nodes are outside this scope.
    Strict queries pin scrape and build-info values and original sample
    timestamps to one evaluation time within three seconds, using at most eight
    fixed job readers. Down, missing,
    stale, future, pre-boundary, conflicting and unknown-revision evidence
    never counts as matching. Freshness is 120 seconds, with 30 seconds of
    clock skew at the boundary. Only complete lowercase 40-character commit
    SHAs are compared. Target addresses and raw labels, build-time strings
    and raw provider errors are excluded; target_id is a SHA-256 digest.
    Counts cover all inspected evidence, even beyond the 32-item preview.
    Mixed or mismatched revisions can coexist with incomplete inventory;
    inspect job availability and all counts. No cached success is returned.

    Args:
        release (str):
        after (datetime.datetime):
        expected_role (GetObsRuntimeBuildsExpectedRole | Unset):
        compare_at (datetime.datetime | Unset):
        diagnostics (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRuntimeBuilds | Problem]
    """

    kwargs = _get_kwargs(
        release=release,
        after=after,
        expected_role=expected_role,
        compare_at=compare_at,
        diagnostics=diagnostics,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    release: str,
    after: datetime.datetime,
    expected_role: GetObsRuntimeBuildsExpectedRole | Unset = UNSET,
    compare_at: datetime.datetime | Unset = UNSET,
    diagnostics: str | Unset = UNSET,
) -> Any | ObsRuntimeBuilds | Problem | None:
    """Inspect advisory running-build evidence for selected critical daemons.

     Requires operator read scope, MFA and the operator allowlist. Read-only,
    advisory identity evidence; this does not qualify or attest a deployment.
    Select a versioned build profile explicitly. V1 profiles single-box-v1
    and control-plane-v1 preserve the original critical jobs. V2 profiles
    single-box-v2 and control-plane-v2 additionally inspect build and
    image workers. Single-box expects their static scrape targets; control-plane
    expects them on every active configured compute-registry member, using
    the existing discovery scope. Telemetry input-health roles stay v1.
    Without a role, no sources are read and the state is unknown. Selected
    build jobs do not cover every daemon in the fleet. Static
    jobs use scrape target membership and a minimum of one. Compute jobs
    compare exact node and node_id labels to the active configured compute
    registry, so replacement nodes cannot hide missing members behind equal
    counts. Draining inactive and unconfigured nodes are outside this scope.
    Strict queries pin scrape and build-info values and original sample
    timestamps to one evaluation time within three seconds, using at most eight
    fixed job readers. Down, missing,
    stale, future, pre-boundary, conflicting and unknown-revision evidence
    never counts as matching. Freshness is 120 seconds, with 30 seconds of
    clock skew at the boundary. Only complete lowercase 40-character commit
    SHAs are compared. Target addresses and raw labels, build-time strings
    and raw provider errors are excluded; target_id is a SHA-256 digest.
    Counts cover all inspected evidence, even beyond the 32-item preview.
    Mixed or mismatched revisions can coexist with incomplete inventory;
    inspect job availability and all counts. No cached success is returned.

    Args:
        release (str):
        after (datetime.datetime):
        expected_role (GetObsRuntimeBuildsExpectedRole | Unset):
        compare_at (datetime.datetime | Unset):
        diagnostics (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRuntimeBuilds | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            release=release,
            after=after,
            expected_role=expected_role,
            compare_at=compare_at,
            diagnostics=diagnostics,
        )
    ).parsed
