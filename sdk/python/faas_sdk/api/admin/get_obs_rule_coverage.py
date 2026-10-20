from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_obs_rule_coverage_expected_set import (
    GetObsRuleCoverageExpectedSet,
)
from ...models.obs_rule_coverage import ObsRuleCoverage
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    expected_set: GetObsRuleCoverageExpectedSet | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_expected_set: str | Unset = UNSET
    if not isinstance(expected_set, Unset):
        json_expected_set = expected_set

    params["expected_set"] = json_expected_set

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/rule-coverage",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsRuleCoverage | Problem | None:
    if response.status_code == 200:
        response_200 = ObsRuleCoverage.from_dict(response.json())

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
) -> Response[Any | ObsRuleCoverage | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRuleCoverageExpectedSet | Unset = UNSET,
) -> Response[Any | ObsRuleCoverage | Problem]:
    """Compare loaded alert definitions with an explicitly selected expected set.

     Requires operator read scope, MFA and the operator allowlist. Without
    expected_set, returns unknown with available versioned set metadata and
    does not read Prometheus. Selection is per request and never establishes
    deployment intent. gregale-core-v1 expects faas.rules.yml alerts;
    gregale-full-v1 also expects bridge.rules.yml, pg_backup.rules.yml and
    data_placement.yaml alerts. Both are minimum sets; additional rules are
    allowed and counted. The embedded catalog revision identifies the exact
    expected definitions shipped in this apid binary.
    A complete bounded Prometheus alert-rule read and comparison share a
    three-second deadline. Compares canonical PromQL syntax, for,
    keep_firing_for and rule labels. Identity uses file basename, group and
    alert name; directory relocation is ignored and basename/group collisions
    are ambiguous. Missing fields and unattributed identities remain unknown.
    A complete empty source is degraded with known missing rules.
    Definitions matching the selected set are covered, independently of
    evaluation, metric input, recording rule or notification health. Group
    intervals, offsets, annotations and source directories are not compared.
    Totals precede a 200-item preview ordered ambiguous, changed, missing,
    unknown, matching, with opaque identity hashes breaking ties. Raw queries,
    labels, errors and absolute paths are omitted. Unselected or unreadable
    evidence returns HTTP 200 with unknown state and null totals. This read
    never changes collection, history or triage state.

    Args:
        expected_set (GetObsRuleCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRuleCoverage | Problem]
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
    expected_set: GetObsRuleCoverageExpectedSet | Unset = UNSET,
) -> Any | ObsRuleCoverage | Problem | None:
    """Compare loaded alert definitions with an explicitly selected expected set.

     Requires operator read scope, MFA and the operator allowlist. Without
    expected_set, returns unknown with available versioned set metadata and
    does not read Prometheus. Selection is per request and never establishes
    deployment intent. gregale-core-v1 expects faas.rules.yml alerts;
    gregale-full-v1 also expects bridge.rules.yml, pg_backup.rules.yml and
    data_placement.yaml alerts. Both are minimum sets; additional rules are
    allowed and counted. The embedded catalog revision identifies the exact
    expected definitions shipped in this apid binary.
    A complete bounded Prometheus alert-rule read and comparison share a
    three-second deadline. Compares canonical PromQL syntax, for,
    keep_firing_for and rule labels. Identity uses file basename, group and
    alert name; directory relocation is ignored and basename/group collisions
    are ambiguous. Missing fields and unattributed identities remain unknown.
    A complete empty source is degraded with known missing rules.
    Definitions matching the selected set are covered, independently of
    evaluation, metric input, recording rule or notification health. Group
    intervals, offsets, annotations and source directories are not compared.
    Totals precede a 200-item preview ordered ambiguous, changed, missing,
    unknown, matching, with opaque identity hashes breaking ties. Raw queries,
    labels, errors and absolute paths are omitted. Unselected or unreadable
    evidence returns HTTP 200 with unknown state and null totals. This read
    never changes collection, history or triage state.

    Args:
        expected_set (GetObsRuleCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRuleCoverage | Problem
    """

    return sync_detailed(
        client=client,
        expected_set=expected_set,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRuleCoverageExpectedSet | Unset = UNSET,
) -> Response[Any | ObsRuleCoverage | Problem]:
    """Compare loaded alert definitions with an explicitly selected expected set.

     Requires operator read scope, MFA and the operator allowlist. Without
    expected_set, returns unknown with available versioned set metadata and
    does not read Prometheus. Selection is per request and never establishes
    deployment intent. gregale-core-v1 expects faas.rules.yml alerts;
    gregale-full-v1 also expects bridge.rules.yml, pg_backup.rules.yml and
    data_placement.yaml alerts. Both are minimum sets; additional rules are
    allowed and counted. The embedded catalog revision identifies the exact
    expected definitions shipped in this apid binary.
    A complete bounded Prometheus alert-rule read and comparison share a
    three-second deadline. Compares canonical PromQL syntax, for,
    keep_firing_for and rule labels. Identity uses file basename, group and
    alert name; directory relocation is ignored and basename/group collisions
    are ambiguous. Missing fields and unattributed identities remain unknown.
    A complete empty source is degraded with known missing rules.
    Definitions matching the selected set are covered, independently of
    evaluation, metric input, recording rule or notification health. Group
    intervals, offsets, annotations and source directories are not compared.
    Totals precede a 200-item preview ordered ambiguous, changed, missing,
    unknown, matching, with opaque identity hashes breaking ties. Raw queries,
    labels, errors and absolute paths are omitted. Unselected or unreadable
    evidence returns HTTP 200 with unknown state and null totals. This read
    never changes collection, history or triage state.

    Args:
        expected_set (GetObsRuleCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsRuleCoverage | Problem]
    """

    kwargs = _get_kwargs(
        expected_set=expected_set,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    expected_set: GetObsRuleCoverageExpectedSet | Unset = UNSET,
) -> Any | ObsRuleCoverage | Problem | None:
    """Compare loaded alert definitions with an explicitly selected expected set.

     Requires operator read scope, MFA and the operator allowlist. Without
    expected_set, returns unknown with available versioned set metadata and
    does not read Prometheus. Selection is per request and never establishes
    deployment intent. gregale-core-v1 expects faas.rules.yml alerts;
    gregale-full-v1 also expects bridge.rules.yml, pg_backup.rules.yml and
    data_placement.yaml alerts. Both are minimum sets; additional rules are
    allowed and counted. The embedded catalog revision identifies the exact
    expected definitions shipped in this apid binary.
    A complete bounded Prometheus alert-rule read and comparison share a
    three-second deadline. Compares canonical PromQL syntax, for,
    keep_firing_for and rule labels. Identity uses file basename, group and
    alert name; directory relocation is ignored and basename/group collisions
    are ambiguous. Missing fields and unattributed identities remain unknown.
    A complete empty source is degraded with known missing rules.
    Definitions matching the selected set are covered, independently of
    evaluation, metric input, recording rule or notification health. Group
    intervals, offsets, annotations and source directories are not compared.
    Totals precede a 200-item preview ordered ambiguous, changed, missing,
    unknown, matching, with opaque identity hashes breaking ties. Raw queries,
    labels, errors and absolute paths are omitted. Unselected or unreadable
    evidence returns HTTP 200 with unknown state and null totals. This read
    never changes collection, history or triage state.

    Args:
        expected_set (GetObsRuleCoverageExpectedSet | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsRuleCoverage | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            expected_set=expected_set,
        )
    ).parsed
