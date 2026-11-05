from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_account_workflow_performance_instances_cohort import (
    ListAccountWorkflowPerformanceInstancesCohort,
)
from ...models.list_account_workflow_performance_instances_condition_evidence_status import (
    ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus,
)
from ...models.list_account_workflow_performance_instances_condition_result import (
    ListAccountWorkflowPerformanceInstancesConditionResult,
)
from ...models.list_account_workflow_performance_instances_dimension import (
    ListAccountWorkflowPerformanceInstancesDimension,
)
from ...models.list_account_workflow_performance_instances_gap_status import (
    ListAccountWorkflowPerformanceInstancesGapStatus,
)
from ...models.list_account_workflow_performance_instances_kind import (
    ListAccountWorkflowPerformanceInstancesKind,
)
from ...models.operation_workflow_performance_instances_response import OperationWorkflowPerformanceInstancesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    workflow: str,
    tenant_id: UUID | Unset = UNSET,
    cohort: ListAccountWorkflowPerformanceInstancesCohort,
    dimension: ListAccountWorkflowPerformanceInstancesDimension,
    baseline_token: str | Unset = UNSET,
    gap_status: ListAccountWorkflowPerformanceInstancesGapStatus | Unset = UNSET,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListAccountWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_evidence_status: ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus | Unset = UNSET,
    condition_result: ListAccountWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params["workflow"] = workflow

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    json_cohort: str = cohort
    params["cohort"] = json_cohort

    json_dimension: str = dimension
    params["dimension"] = json_dimension

    params["baseline_token"] = baseline_token

    json_gap_status: str | Unset = UNSET
    if not isinstance(gap_status, Unset):
        json_gap_status = gap_status

    params["gap_status"] = json_gap_status

    params["contract_version"] = contract_version

    params["state"] = state

    params["operation"] = operation

    params["code"] = code

    params["owner"] = owner

    params["unassigned"] = unassigned

    params["cohort_token"] = cohort_token

    json_kind: str | Unset = UNSET
    if not isinstance(kind, Unset):
        json_kind = kind

    params["kind"] = json_kind

    params["decision_code"] = decision_code

    params["rule_id"] = rule_id

    params["rule_version"] = rule_version

    params["group_id"] = group_id

    params["path_id"] = path_id

    params["condition_id"] = condition_id

    params["condition_description"] = condition_description

    json_condition_evidence_status: str | Unset = UNSET
    if not isinstance(condition_evidence_status, Unset):
        json_condition_evidence_status = condition_evidence_status

    params["condition_evidence_status"] = json_condition_evidence_status

    json_condition_result: str | Unset = UNSET
    if not isinstance(condition_result, Unset):
        json_condition_result = condition_result

    params["condition_result"] = json_condition_result

    params["outcome_code"] = outcome_code

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-performance/instances".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowPerformanceInstancesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowPerformanceInstancesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowPerformanceInstancesResponse | Problem]:
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
    scope: str,
    workflow: str,
    tenant_id: UUID | Unset = UNSET,
    cohort: ListAccountWorkflowPerformanceInstancesCohort,
    dimension: ListAccountWorkflowPerformanceInstancesDimension,
    baseline_token: str | Unset = UNSET,
    gap_status: ListAccountWorkflowPerformanceInstancesGapStatus | Unset = UNSET,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListAccountWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_evidence_status: ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus | Unset = UNSET,
    condition_result: ListAccountWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> Response[OperationWorkflowPerformanceInstancesResponse | Problem]:
    """List ranked contributors to a workflow performance group.

     Requires account read scope and MFA. Optional tenant selection stays within account ownership.
    Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest
    100 instances per cohort and complete-history eligibility as the performance summary. State groups
    require state and contract_version; blocker groups require operation/code/contract_version and
    exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or
    unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the
    evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and
    require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every
    matching eligible contributor up to 100 sorted by observed duration descending with stable identity
    ties. No cursor is supported. Current blockers can differ from historical contributing groups;
    pending verification previews are bounded independently of exact counts. Decision dimensions require
    completed cohort and cohort_token, an exact kind/contract_version/outcome_code and
    decision_code/rule_id/rule_version for decision groups. Path groups additionally require
    group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group
    must be present in the bounded decision_outcomes summary. Decision dimensions reject
    state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision
    fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions
    also require completed cohort and cohort_token and an exact returned condition_outcomes group
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown. Active condition
    dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner
    selectors are rejected. They use latest explicit decision snapshots in the current contract, return
    evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration.
    Metadata-only reports preserve evidence; explicit condition omission clears prior results. The
    optional condition_evidence_status selector restricts active contributors to within_budget, overdue
    or no_budget. Status counters partition each returned group and the chosen status must have
    contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report
    age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort
    and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly
    one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and
    decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and
    include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the
    preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing
    cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at
    least one overdue selected condition once, across all refresh owners. The former ranks by oldest
    overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow
    identity breaks ties. These overall rankings remain available with zero contributors and are
    independent of condition and owner display caps. Each contributor includes refresh_priority with the
    oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus
    the bounded condition preview. Age ranking does not mean most past deadline or highest business
    severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner
    dimension requires ongoing cohort and cohort_token and an exact returned
    condition_coverage.missing_groups row selected by contract_version, current state and exactly one of
    owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors
    are rejected. Counts and eligibility use full condition coverage before previews. Each workflow
    appears once in its selected group and ranks by full observed workflow blocked duration with stable
    identity ties. Missing evidence has no report timestamp and is not failed verification; expectations
    come from the latest reporting Operation pinned definition. Missing condition entries and pinned
    guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner
    dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The
    baseline must not be later than the current token evaluation. Select an exact returned
    new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of
    owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner
    and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews;
    each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence
    and pinned guidance. Current report comparisons permit contract boundaries while baseline history
    must be continuous within its contract; ranking uses full observed report-only blocked intervals
    without changing eligibility for existing duration groups. Generic duration coverage remains
    separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing
    cohort and both cohort_token and baseline_token with the same comparison scope and baseline time
    rules. It returns each eligible workflow once, independent of displayed group caps. Optional
    gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be
    omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty
    results are valid. Each contributor includes gap_queue_priority with all category counts, sorted
    assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking
    explanation before filters or previews. gap_progress counts and previews contain only filtered gaps.
    Ranking measures observed report-only blocked intervals, omitting intervals spanning contract
    boundaries, with stable workflow identity ties.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        baseline_token (str | Unset):
        gap_status (ListAccountWorkflowPerformanceInstancesGapStatus | Unset):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListAccountWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_evidence_status (ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus
            | Unset):
        condition_result (ListAccountWorkflowPerformanceInstancesConditionResult | Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPerformanceInstancesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        workflow=workflow,
        tenant_id=tenant_id,
        cohort=cohort,
        dimension=dimension,
        baseline_token=baseline_token,
        gap_status=gap_status,
        contract_version=contract_version,
        state=state,
        operation=operation,
        code=code,
        owner=owner,
        unassigned=unassigned,
        cohort_token=cohort_token,
        kind=kind,
        decision_code=decision_code,
        rule_id=rule_id,
        rule_version=rule_version,
        group_id=group_id,
        path_id=path_id,
        condition_id=condition_id,
        condition_description=condition_description,
        condition_evidence_status=condition_evidence_status,
        condition_result=condition_result,
        outcome_code=outcome_code,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str,
    tenant_id: UUID | Unset = UNSET,
    cohort: ListAccountWorkflowPerformanceInstancesCohort,
    dimension: ListAccountWorkflowPerformanceInstancesDimension,
    baseline_token: str | Unset = UNSET,
    gap_status: ListAccountWorkflowPerformanceInstancesGapStatus | Unset = UNSET,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListAccountWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_evidence_status: ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus | Unset = UNSET,
    condition_result: ListAccountWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> OperationWorkflowPerformanceInstancesResponse | Problem | None:
    """List ranked contributors to a workflow performance group.

     Requires account read scope and MFA. Optional tenant selection stays within account ownership.
    Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest
    100 instances per cohort and complete-history eligibility as the performance summary. State groups
    require state and contract_version; blocker groups require operation/code/contract_version and
    exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or
    unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the
    evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and
    require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every
    matching eligible contributor up to 100 sorted by observed duration descending with stable identity
    ties. No cursor is supported. Current blockers can differ from historical contributing groups;
    pending verification previews are bounded independently of exact counts. Decision dimensions require
    completed cohort and cohort_token, an exact kind/contract_version/outcome_code and
    decision_code/rule_id/rule_version for decision groups. Path groups additionally require
    group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group
    must be present in the bounded decision_outcomes summary. Decision dimensions reject
    state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision
    fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions
    also require completed cohort and cohort_token and an exact returned condition_outcomes group
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown. Active condition
    dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner
    selectors are rejected. They use latest explicit decision snapshots in the current contract, return
    evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration.
    Metadata-only reports preserve evidence; explicit condition omission clears prior results. The
    optional condition_evidence_status selector restricts active contributors to within_budget, overdue
    or no_budget. Status counters partition each returned group and the chosen status must have
    contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report
    age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort
    and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly
    one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and
    decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and
    include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the
    preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing
    cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at
    least one overdue selected condition once, across all refresh owners. The former ranks by oldest
    overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow
    identity breaks ties. These overall rankings remain available with zero contributors and are
    independent of condition and owner display caps. Each contributor includes refresh_priority with the
    oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus
    the bounded condition preview. Age ranking does not mean most past deadline or highest business
    severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner
    dimension requires ongoing cohort and cohort_token and an exact returned
    condition_coverage.missing_groups row selected by contract_version, current state and exactly one of
    owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors
    are rejected. Counts and eligibility use full condition coverage before previews. Each workflow
    appears once in its selected group and ranks by full observed workflow blocked duration with stable
    identity ties. Missing evidence has no report timestamp and is not failed verification; expectations
    come from the latest reporting Operation pinned definition. Missing condition entries and pinned
    guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner
    dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The
    baseline must not be later than the current token evaluation. Select an exact returned
    new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of
    owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner
    and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews;
    each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence
    and pinned guidance. Current report comparisons permit contract boundaries while baseline history
    must be continuous within its contract; ranking uses full observed report-only blocked intervals
    without changing eligibility for existing duration groups. Generic duration coverage remains
    separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing
    cohort and both cohort_token and baseline_token with the same comparison scope and baseline time
    rules. It returns each eligible workflow once, independent of displayed group caps. Optional
    gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be
    omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty
    results are valid. Each contributor includes gap_queue_priority with all category counts, sorted
    assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking
    explanation before filters or previews. gap_progress counts and previews contain only filtered gaps.
    Ranking measures observed report-only blocked intervals, omitting intervals spanning contract
    boundaries, with stable workflow identity ties.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        baseline_token (str | Unset):
        gap_status (ListAccountWorkflowPerformanceInstancesGapStatus | Unset):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListAccountWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_evidence_status (ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus
            | Unset):
        condition_result (ListAccountWorkflowPerformanceInstancesConditionResult | Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPerformanceInstancesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        workflow=workflow,
        tenant_id=tenant_id,
        cohort=cohort,
        dimension=dimension,
        baseline_token=baseline_token,
        gap_status=gap_status,
        contract_version=contract_version,
        state=state,
        operation=operation,
        code=code,
        owner=owner,
        unassigned=unassigned,
        cohort_token=cohort_token,
        kind=kind,
        decision_code=decision_code,
        rule_id=rule_id,
        rule_version=rule_version,
        group_id=group_id,
        path_id=path_id,
        condition_id=condition_id,
        condition_description=condition_description,
        condition_evidence_status=condition_evidence_status,
        condition_result=condition_result,
        outcome_code=outcome_code,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str,
    tenant_id: UUID | Unset = UNSET,
    cohort: ListAccountWorkflowPerformanceInstancesCohort,
    dimension: ListAccountWorkflowPerformanceInstancesDimension,
    baseline_token: str | Unset = UNSET,
    gap_status: ListAccountWorkflowPerformanceInstancesGapStatus | Unset = UNSET,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListAccountWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_evidence_status: ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus | Unset = UNSET,
    condition_result: ListAccountWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> Response[OperationWorkflowPerformanceInstancesResponse | Problem]:
    """List ranked contributors to a workflow performance group.

     Requires account read scope and MFA. Optional tenant selection stays within account ownership.
    Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest
    100 instances per cohort and complete-history eligibility as the performance summary. State groups
    require state and contract_version; blocker groups require operation/code/contract_version and
    exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or
    unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the
    evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and
    require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every
    matching eligible contributor up to 100 sorted by observed duration descending with stable identity
    ties. No cursor is supported. Current blockers can differ from historical contributing groups;
    pending verification previews are bounded independently of exact counts. Decision dimensions require
    completed cohort and cohort_token, an exact kind/contract_version/outcome_code and
    decision_code/rule_id/rule_version for decision groups. Path groups additionally require
    group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group
    must be present in the bounded decision_outcomes summary. Decision dimensions reject
    state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision
    fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions
    also require completed cohort and cohort_token and an exact returned condition_outcomes group
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown. Active condition
    dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner
    selectors are rejected. They use latest explicit decision snapshots in the current contract, return
    evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration.
    Metadata-only reports preserve evidence; explicit condition omission clears prior results. The
    optional condition_evidence_status selector restricts active contributors to within_budget, overdue
    or no_budget. Status counters partition each returned group and the chosen status must have
    contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report
    age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort
    and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly
    one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and
    decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and
    include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the
    preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing
    cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at
    least one overdue selected condition once, across all refresh owners. The former ranks by oldest
    overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow
    identity breaks ties. These overall rankings remain available with zero contributors and are
    independent of condition and owner display caps. Each contributor includes refresh_priority with the
    oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus
    the bounded condition preview. Age ranking does not mean most past deadline or highest business
    severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner
    dimension requires ongoing cohort and cohort_token and an exact returned
    condition_coverage.missing_groups row selected by contract_version, current state and exactly one of
    owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors
    are rejected. Counts and eligibility use full condition coverage before previews. Each workflow
    appears once in its selected group and ranks by full observed workflow blocked duration with stable
    identity ties. Missing evidence has no report timestamp and is not failed verification; expectations
    come from the latest reporting Operation pinned definition. Missing condition entries and pinned
    guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner
    dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The
    baseline must not be later than the current token evaluation. Select an exact returned
    new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of
    owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner
    and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews;
    each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence
    and pinned guidance. Current report comparisons permit contract boundaries while baseline history
    must be continuous within its contract; ranking uses full observed report-only blocked intervals
    without changing eligibility for existing duration groups. Generic duration coverage remains
    separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing
    cohort and both cohort_token and baseline_token with the same comparison scope and baseline time
    rules. It returns each eligible workflow once, independent of displayed group caps. Optional
    gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be
    omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty
    results are valid. Each contributor includes gap_queue_priority with all category counts, sorted
    assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking
    explanation before filters or previews. gap_progress counts and previews contain only filtered gaps.
    Ranking measures observed report-only blocked intervals, omitting intervals spanning contract
    boundaries, with stable workflow identity ties.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        baseline_token (str | Unset):
        gap_status (ListAccountWorkflowPerformanceInstancesGapStatus | Unset):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListAccountWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_evidence_status (ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus
            | Unset):
        condition_result (ListAccountWorkflowPerformanceInstancesConditionResult | Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPerformanceInstancesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        workflow=workflow,
        tenant_id=tenant_id,
        cohort=cohort,
        dimension=dimension,
        baseline_token=baseline_token,
        gap_status=gap_status,
        contract_version=contract_version,
        state=state,
        operation=operation,
        code=code,
        owner=owner,
        unassigned=unassigned,
        cohort_token=cohort_token,
        kind=kind,
        decision_code=decision_code,
        rule_id=rule_id,
        rule_version=rule_version,
        group_id=group_id,
        path_id=path_id,
        condition_id=condition_id,
        condition_description=condition_description,
        condition_evidence_status=condition_evidence_status,
        condition_result=condition_result,
        outcome_code=outcome_code,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str,
    tenant_id: UUID | Unset = UNSET,
    cohort: ListAccountWorkflowPerformanceInstancesCohort,
    dimension: ListAccountWorkflowPerformanceInstancesDimension,
    baseline_token: str | Unset = UNSET,
    gap_status: ListAccountWorkflowPerformanceInstancesGapStatus | Unset = UNSET,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListAccountWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_evidence_status: ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus | Unset = UNSET,
    condition_result: ListAccountWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> OperationWorkflowPerformanceInstancesResponse | Problem | None:
    """List ranked contributors to a workflow performance group.

     Requires account read scope and MFA. Optional tenant selection stays within account ownership.
    Explicit app/environment/workflow and cohort/dimension selection are required. Uses the same latest
    100 instances per cohort and complete-history eligibility as the performance summary. State groups
    require state and contract_version; blocker groups require operation/code/contract_version and
    exactly one of owner or unassigned=true; verification_owner groups require exactly one of owner or
    unassigned=true. Overall dimensions reject group selectors. An optional cohort_token binds the
    evaluation time and retained evidence to a prior summary; changed or expired cohorts return 409 and
    require refreshing the summary. Without a token this evaluates a fresh cohort. Returns every
    matching eligible contributor up to 100 sorted by observed duration descending with stable identity
    ties. No cursor is supported. Current blockers can differ from historical contributing groups;
    pending verification previews are bounded independently of exact counts. Decision dimensions require
    completed cohort and cohort_token, an exact kind/contract_version/outcome_code and
    decision_code/rule_id/rule_version for decision groups. Path groups additionally require
    group_id/path_id; the decision triple is either fully supplied or fully omitted. The exact group
    must be present in the bounded decision_outcomes summary. Decision dimensions reject
    state/blocker/owner selectors. Returns the earliest qualifying report and optional business decision
    fact per contributor; reports after the first terminal revision do not qualify. Condition dimensions
    also require completed cohort and cohort_token and an exact returned condition_outcomes group
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown. Active condition
    dimensions require ongoing cohort and cohort_token, an exact returned active_conditions identity
    selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result. Outcome and kind/path/state/blocker/owner
    selectors are rejected. They use latest explicit decision snapshots in the current contract, return
    evidence age at the bound evaluation time and measure whole-workflow elapsed or blocked duration.
    Metadata-only reports preserve evidence; explicit condition omission clears prior results. The
    optional condition_evidence_status selector restricts active contributors to within_budget, overdue
    or no_budget. Status counters partition each returned group and the chosen status must have
    contributors. Budgets come from the reporting Operation pinned definition; deadlines measure report
    age, not business verification age. The condition_refresh_owner dimension requires ongoing cohort
    and cohort_token and an exact returned active_conditions.refresh_owners group selected by exactly
    one of owner or unassigned=true. It rejects contract/state/blocker/condition/result/status and
    decision/path/outcome selectors. Contributors are ranked by whole-workflow blocked duration and
    include bounded overdue condition facts with pinned refresh guidance; counts remain exact beyond the
    preview limit. The condition_refresh_age and condition_refresh_blocked dimensions require ongoing
    cohort and cohort_token and reject all group selectors. Each returns eligible workflows with at
    least one overdue selected condition once, across all refresh owners. The former ranks by oldest
    overdue report age in whole seconds, the latter by full workflow blocked duration; stable workflow
    identity breaks ties. These overall rankings remain available with zero contributors and are
    independent of condition and owner display caps. Each contributor includes refresh_priority with the
    oldest qualifying fact, complete assigned owner list, exact unassigned count and explanation, plus
    the bounded condition preview. Age ranking does not mean most past deadline or highest business
    severity. Conflicting snapshots and incomplete histories are excluded. The condition_missing_owner
    dimension requires ongoing cohort and cohort_token and an exact returned
    condition_coverage.missing_groups row selected by contract_version, current state and exactly one of
    owner or unassigned=true. All condition/result/status, decision/path/outcome and blocker selectors
    are rejected. Counts and eligibility use full condition coverage before previews. Each workflow
    appears once in its selected group and ranks by full observed workflow blocked duration with stable
    identity ties. Missing evidence has no report timestamp and is not failed verification; expectations
    come from the latest reporting Operation pinned definition. Missing condition entries and pinned
    guidance are previewed up to 16 with exact counts and explicit truncation. The condition_gap_owner
    dimension requires ongoing cohort, cohort_token and baseline_token bound to the same scope. The
    baseline must not be later than the current token evaluation. Select an exact returned
    new_missing_progress.groups row with contract_version, current state, gap_status and exactly one of
    owner or unassigned=true. All other selectors are rejected; dimensions outside condition_gap_owner
    and condition_gap_queue reject baseline_token and gap_status. Counts and grouping precede previews;
    each contributor includes up to 16 matching gap_progress changes with exact counts, prior evidence
    and pinned guidance. Current report comparisons permit contract boundaries while baseline history
    must be continuous within its contract; ranking uses full observed report-only blocked intervals
    without changing eligibility for existing duration groups. Generic duration coverage remains
    separate from new_missing_progress coverage. The condition_gap_queue dimension requires ongoing
    cohort and both cohort_token and baseline_token with the same comparison scope and baseline time
    rules. It returns each eligible workflow once, independent of displayed group caps. Optional
    gap_status and exactly one of owner or unassigned=true filter matching current gaps; both may be
    omitted. Contract/state/blocker/condition/decision/path/outcome selectors are rejected. Empty
    results are valid. Each contributor includes gap_queue_priority with all category counts, sorted
    assigned owners, exact unassigned count, all pinned missing expectation guidance and a ranking
    explanation before filters or previews. gap_progress counts and previews contain only filtered gaps.
    Ranking measures observed report-only blocked intervals, omitting intervals spanning contract
    boundaries, with stable workflow identity ties.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        baseline_token (str | Unset):
        gap_status (ListAccountWorkflowPerformanceInstancesGapStatus | Unset):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListAccountWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_evidence_status (ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus
            | Unset):
        condition_result (ListAccountWorkflowPerformanceInstancesConditionResult | Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPerformanceInstancesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            workflow=workflow,
            tenant_id=tenant_id,
            cohort=cohort,
            dimension=dimension,
            baseline_token=baseline_token,
            gap_status=gap_status,
            contract_version=contract_version,
            state=state,
            operation=operation,
            code=code,
            owner=owner,
            unassigned=unassigned,
            cohort_token=cohort_token,
            kind=kind,
            decision_code=decision_code,
            rule_id=rule_id,
            rule_version=rule_version,
            group_id=group_id,
            path_id=path_id,
            condition_id=condition_id,
            condition_description=condition_description,
            condition_evidence_status=condition_evidence_status,
            condition_result=condition_result,
            outcome_code=outcome_code,
        )
    ).parsed
