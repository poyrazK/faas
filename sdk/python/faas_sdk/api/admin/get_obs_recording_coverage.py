from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_obs_recording_coverage_expected_set import (
    GetObsRecordingCoverageExpectedSet,
)
from ...models.obs_recording_coverage import ObsRecordingCoverage
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    expected_set: GetObsRecordingCoverageExpectedSet | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_expected_set: str | Unset = UNSET
    if not isinstance(expected_set, Unset):
        json_expected_set = expected_set

    params["expected_set"] = json_expected_set

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/recording-coverage",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsRecordingCoverage | Problem | None:
    if response.status_code == 200:
        response_200 = ObsRecordingCoverage.from_dict(response.json())

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
) -> Response[Any | ObsRecordingCoverage | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRecordingCoverageExpectedSet | Unset = UNSET,
) -> Response[Any | ObsRecordingCoverage | Problem]:
    """Compare expected recording definitions and their catalog alert references.

     Requires operator read scope, MFA and the operator allowlist. Select a
    minimum expected file set explicitly with expected_set. Core selects
    faas.rules.yml; full also selects bridge.rules.yml, pg_backup.rules.yml
    and data_placement.yaml. Without selection, returns unknown with available
    metadata and makes no Prometheus request. Catalog presence is not
    deployment intent. This recording catalog has its own exact revision.
    Reads one complete bounded /api/v1/rules?type=record source and compares
    canonical expressions and rule labels under a shared three-second budget.
    Omitted recording labels mean empty per the Prometheus wire contract;
    explicit null labels or unsupported definitions remain unknown. Identity
    uses file basename, group and recording name. Duplicate candidates or
    basename/group collisions are ambiguous. Additional valid identities are
    allowed. A complete empty source has known missing expected definitions.
    Definition totals precede a 200-item preview ordered ambiguous, changed,
    missing, unknown, matching and then identity hash. Recording comparisons
    only report expression or labels as changed fields.
    Each item includes at most five syntactically dependent expected alerts,
    including transitive recording chains, with known-reference count and truncation.
    Dependency metadata binds the exact expected alert catalog revision.
    Nonexact metric selectors, duplicate producer names and reachable cycles
    leave explicit mapping uncertainty; its alert preview is also capped at
    five with full counts. Known references can coexist with uncertain links.
    Dependencies describe selected expected catalog syntax, not current
    deployed alert definitions, series presence, execution impact or cause.
    Covered definitions do not prove evaluation or input or notification
    health. Group intervals, offsets and annotations are outside comparison.
    Source failures return unknown with null totals; selected catalog mapping
    remains independently available. Raw queries, labels, errors, source URLs
    and absolute paths are omitted. No collection, history or triage writes.

    Args:
        expected_set (GetObsRecordingCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRecordingCoverage | Problem]
    """

    kwargs = _get_kwargs(
        expected_set=expected_set,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRecordingCoverageExpectedSet | Unset = UNSET,
) -> Any | ObsRecordingCoverage | Problem | None:
    """Compare expected recording definitions and their catalog alert references.

     Requires operator read scope, MFA and the operator allowlist. Select a
    minimum expected file set explicitly with expected_set. Core selects
    faas.rules.yml; full also selects bridge.rules.yml, pg_backup.rules.yml
    and data_placement.yaml. Without selection, returns unknown with available
    metadata and makes no Prometheus request. Catalog presence is not
    deployment intent. This recording catalog has its own exact revision.
    Reads one complete bounded /api/v1/rules?type=record source and compares
    canonical expressions and rule labels under a shared three-second budget.
    Omitted recording labels mean empty per the Prometheus wire contract;
    explicit null labels or unsupported definitions remain unknown. Identity
    uses file basename, group and recording name. Duplicate candidates or
    basename/group collisions are ambiguous. Additional valid identities are
    allowed. A complete empty source has known missing expected definitions.
    Definition totals precede a 200-item preview ordered ambiguous, changed,
    missing, unknown, matching and then identity hash. Recording comparisons
    only report expression or labels as changed fields.
    Each item includes at most five syntactically dependent expected alerts,
    including transitive recording chains, with known-reference count and truncation.
    Dependency metadata binds the exact expected alert catalog revision.
    Nonexact metric selectors, duplicate producer names and reachable cycles
    leave explicit mapping uncertainty; its alert preview is also capped at
    five with full counts. Known references can coexist with uncertain links.
    Dependencies describe selected expected catalog syntax, not current
    deployed alert definitions, series presence, execution impact or cause.
    Covered definitions do not prove evaluation or input or notification
    health. Group intervals, offsets and annotations are outside comparison.
    Source failures return unknown with null totals; selected catalog mapping
    remains independently available. Raw queries, labels, errors, source URLs
    and absolute paths are omitted. No collection, history or triage writes.

    Args:
        expected_set (GetObsRecordingCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRecordingCoverage | Problem
    """

    return sync_detailed(
        client=client,
        expected_set=expected_set,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRecordingCoverageExpectedSet | Unset = UNSET,
) -> Response[Any | ObsRecordingCoverage | Problem]:
    """Compare expected recording definitions and their catalog alert references.

     Requires operator read scope, MFA and the operator allowlist. Select a
    minimum expected file set explicitly with expected_set. Core selects
    faas.rules.yml; full also selects bridge.rules.yml, pg_backup.rules.yml
    and data_placement.yaml. Without selection, returns unknown with available
    metadata and makes no Prometheus request. Catalog presence is not
    deployment intent. This recording catalog has its own exact revision.
    Reads one complete bounded /api/v1/rules?type=record source and compares
    canonical expressions and rule labels under a shared three-second budget.
    Omitted recording labels mean empty per the Prometheus wire contract;
    explicit null labels or unsupported definitions remain unknown. Identity
    uses file basename, group and recording name. Duplicate candidates or
    basename/group collisions are ambiguous. Additional valid identities are
    allowed. A complete empty source has known missing expected definitions.
    Definition totals precede a 200-item preview ordered ambiguous, changed,
    missing, unknown, matching and then identity hash. Recording comparisons
    only report expression or labels as changed fields.
    Each item includes at most five syntactically dependent expected alerts,
    including transitive recording chains, with known-reference count and truncation.
    Dependency metadata binds the exact expected alert catalog revision.
    Nonexact metric selectors, duplicate producer names and reachable cycles
    leave explicit mapping uncertainty; its alert preview is also capped at
    five with full counts. Known references can coexist with uncertain links.
    Dependencies describe selected expected catalog syntax, not current
    deployed alert definitions, series presence, execution impact or cause.
    Covered definitions do not prove evaluation or input or notification
    health. Group intervals, offsets and annotations are outside comparison.
    Source failures return unknown with null totals; selected catalog mapping
    remains independently available. Raw queries, labels, errors, source URLs
    and absolute paths are omitted. No collection, history or triage writes.

    Args:
        expected_set (GetObsRecordingCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRecordingCoverage | Problem]
    """

    kwargs = _get_kwargs(
        expected_set=expected_set,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRecordingCoverageExpectedSet | Unset = UNSET,
) -> Any | ObsRecordingCoverage | Problem | None:
    """Compare expected recording definitions and their catalog alert references.

     Requires operator read scope, MFA and the operator allowlist. Select a
    minimum expected file set explicitly with expected_set. Core selects
    faas.rules.yml; full also selects bridge.rules.yml, pg_backup.rules.yml
    and data_placement.yaml. Without selection, returns unknown with available
    metadata and makes no Prometheus request. Catalog presence is not
    deployment intent. This recording catalog has its own exact revision.
    Reads one complete bounded /api/v1/rules?type=record source and compares
    canonical expressions and rule labels under a shared three-second budget.
    Omitted recording labels mean empty per the Prometheus wire contract;
    explicit null labels or unsupported definitions remain unknown. Identity
    uses file basename, group and recording name. Duplicate candidates or
    basename/group collisions are ambiguous. Additional valid identities are
    allowed. A complete empty source has known missing expected definitions.
    Definition totals precede a 200-item preview ordered ambiguous, changed,
    missing, unknown, matching and then identity hash. Recording comparisons
    only report expression or labels as changed fields.
    Each item includes at most five syntactically dependent expected alerts,
    including transitive recording chains, with known-reference count and truncation.
    Dependency metadata binds the exact expected alert catalog revision.
    Nonexact metric selectors, duplicate producer names and reachable cycles
    leave explicit mapping uncertainty; its alert preview is also capped at
    five with full counts. Known references can coexist with uncertain links.
    Dependencies describe selected expected catalog syntax, not current
    deployed alert definitions, series presence, execution impact or cause.
    Covered definitions do not prove evaluation or input or notification
    health. Group intervals, offsets and annotations are outside comparison.
    Source failures return unknown with null totals; selected catalog mapping
    remains independently available. Raw queries, labels, errors, source URLs
    and absolute paths are omitted. No collection, history or triage writes.

    Args:
        expected_set (GetObsRecordingCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRecordingCoverage | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            expected_set=expected_set,
        )
    ).parsed
