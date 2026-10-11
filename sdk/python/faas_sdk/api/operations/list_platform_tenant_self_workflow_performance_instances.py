from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_platform_tenant_self_workflow_performance_instances_cohort import (
    ListPlatformTenantSelfWorkflowPerformanceInstancesCohort,
)
from ...models.list_platform_tenant_self_workflow_performance_instances_condition_result import (
    ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult,
)
from ...models.list_platform_tenant_self_workflow_performance_instances_dimension import (
    ListPlatformTenantSelfWorkflowPerformanceInstancesDimension,
)
from ...models.list_platform_tenant_self_workflow_performance_instances_kind import (
    ListPlatformTenantSelfWorkflowPerformanceInstancesKind,
)
from ...models.operation_workflow_performance_instances_response import OperationWorkflowPerformanceInstancesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    workflow: str,
    cohort: ListPlatformTenantSelfWorkflowPerformanceInstancesCohort,
    dimension: ListPlatformTenantSelfWorkflowPerformanceInstancesDimension,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_result: ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["workflow"] = workflow

    json_cohort: str = cohort
    params["cohort"] = json_cohort

    json_dimension: str = dimension
    params["dimension"] = json_dimension

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

    json_condition_result: str | Unset = UNSET
    if not isinstance(condition_result, Unset):
        json_condition_result = condition_result

    params["condition_result"] = json_condition_result

    params["outcome_code"] = outcome_code

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-performance/instances",
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
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
    cohort: ListPlatformTenantSelfWorkflowPerformanceInstancesCohort,
    dimension: ListPlatformTenantSelfWorkflowPerformanceInstancesDimension,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_result: ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> Response[OperationWorkflowPerformanceInstancesResponse | Problem]:
    """List ranked performance contributors for the authenticated customer.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app/environment/workflow and cohort/dimension selection are required. Uses
    the same latest 100 instances per cohort and complete-history eligibility as the performance
    summary. State groups require state and contract_version; blocker groups require
    operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner
    groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors.
    An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed
    or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a
    fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration
    descending with stable identity ties. No cursor is supported. Current blockers can differ from
    historical contributing groups; pending verification previews are bounded independently of exact
    counts. Decision dimensions require completed cohort and cohort_token, an exact
    kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path
    groups additionally require group_id/path_id; the decision triple is either fully supplied or fully
    omitted. The exact group must be present in the bounded decision_outcomes summary. Decision
    dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional
    business decision fact per contributor; reports after the first terminal revision do not qualify.
    Condition dimensions also require completed cohort and cohort_token and an exact returned
    condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):
        cohort (ListPlatformTenantSelfWorkflowPerformanceInstancesCohort):
        dimension (ListPlatformTenantSelfWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_result (ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult |
            Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPerformanceInstancesResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        cohort=cohort,
        dimension=dimension,
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
        condition_result=condition_result,
        outcome_code=outcome_code,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
    cohort: ListPlatformTenantSelfWorkflowPerformanceInstancesCohort,
    dimension: ListPlatformTenantSelfWorkflowPerformanceInstancesDimension,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_result: ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> OperationWorkflowPerformanceInstancesResponse | Problem | None:
    """List ranked performance contributors for the authenticated customer.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app/environment/workflow and cohort/dimension selection are required. Uses
    the same latest 100 instances per cohort and complete-history eligibility as the performance
    summary. State groups require state and contract_version; blocker groups require
    operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner
    groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors.
    An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed
    or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a
    fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration
    descending with stable identity ties. No cursor is supported. Current blockers can differ from
    historical contributing groups; pending verification previews are bounded independently of exact
    counts. Decision dimensions require completed cohort and cohort_token, an exact
    kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path
    groups additionally require group_id/path_id; the decision triple is either fully supplied or fully
    omitted. The exact group must be present in the bounded decision_outcomes summary. Decision
    dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional
    business decision fact per contributor; reports after the first terminal revision do not qualify.
    Condition dimensions also require completed cohort and cohort_token and an exact returned
    condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):
        cohort (ListPlatformTenantSelfWorkflowPerformanceInstancesCohort):
        dimension (ListPlatformTenantSelfWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_result (ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult |
            Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPerformanceInstancesResponse | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        cohort=cohort,
        dimension=dimension,
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
        condition_result=condition_result,
        outcome_code=outcome_code,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
    cohort: ListPlatformTenantSelfWorkflowPerformanceInstancesCohort,
    dimension: ListPlatformTenantSelfWorkflowPerformanceInstancesDimension,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_result: ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> Response[OperationWorkflowPerformanceInstancesResponse | Problem]:
    """List ranked performance contributors for the authenticated customer.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app/environment/workflow and cohort/dimension selection are required. Uses
    the same latest 100 instances per cohort and complete-history eligibility as the performance
    summary. State groups require state and contract_version; blocker groups require
    operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner
    groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors.
    An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed
    or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a
    fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration
    descending with stable identity ties. No cursor is supported. Current blockers can differ from
    historical contributing groups; pending verification previews are bounded independently of exact
    counts. Decision dimensions require completed cohort and cohort_token, an exact
    kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path
    groups additionally require group_id/path_id; the decision triple is either fully supplied or fully
    omitted. The exact group must be present in the bounded decision_outcomes summary. Decision
    dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional
    business decision fact per contributor; reports after the first terminal revision do not qualify.
    Condition dimensions also require completed cohort and cohort_token and an exact returned
    condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):
        cohort (ListPlatformTenantSelfWorkflowPerformanceInstancesCohort):
        dimension (ListPlatformTenantSelfWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_result (ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult |
            Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPerformanceInstancesResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        cohort=cohort,
        dimension=dimension,
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
        condition_result=condition_result,
        outcome_code=outcome_code,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
    cohort: ListPlatformTenantSelfWorkflowPerformanceInstancesCohort,
    dimension: ListPlatformTenantSelfWorkflowPerformanceInstancesDimension,
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
    kind: ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset = UNSET,
    decision_code: str | Unset = UNSET,
    rule_id: str | Unset = UNSET,
    rule_version: str | Unset = UNSET,
    group_id: str | Unset = UNSET,
    path_id: str | Unset = UNSET,
    condition_id: str | Unset = UNSET,
    condition_description: str | Unset = UNSET,
    condition_result: ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult | Unset = UNSET,
    outcome_code: str | Unset = UNSET,
) -> OperationWorkflowPerformanceInstancesResponse | Problem | None:
    """List ranked performance contributors for the authenticated customer.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app/environment/workflow and cohort/dimension selection are required. Uses
    the same latest 100 instances per cohort and complete-history eligibility as the performance
    summary. State groups require state and contract_version; blocker groups require
    operation/code/contract_version and exactly one of owner or unassigned=true; verification_owner
    groups require exactly one of owner or unassigned=true. Overall dimensions reject group selectors.
    An optional cohort_token binds the evaluation time and retained evidence to a prior summary; changed
    or expired cohorts return 409 and require refreshing the summary. Without a token this evaluates a
    fresh cohort. Returns every matching eligible contributor up to 100 sorted by observed duration
    descending with stable identity ties. No cursor is supported. Current blockers can differ from
    historical contributing groups; pending verification previews are bounded independently of exact
    counts. Decision dimensions require completed cohort and cohort_token, an exact
    kind/contract_version/outcome_code and decision_code/rule_id/rule_version for decision groups. Path
    groups additionally require group_id/path_id; the decision triple is either fully supplied or fully
    omitted. The exact group must be present in the bounded decision_outcomes summary. Decision
    dimensions reject state/blocker/owner selectors. Returns the earliest qualifying report and optional
    business decision fact per contributor; reports after the first terminal revision do not qualify.
    Condition dimensions also require completed cohort and cohort_token and an exact returned
    condition_outcomes group selected by contract_version and decision_code/rule_id/rule_version and
    condition_id/condition_description/condition_result and outcome_code. They reject
    kind/path/state/blocker/owner selectors. Each workflow contributes once per observed result; changed
    results can produce overlapping groups. The earliest qualifying fact and all results observed for
    that exact identity are returned. Missing conditions are not classified as unknown.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):
        cohort (ListPlatformTenantSelfWorkflowPerformanceInstancesCohort):
        dimension (ListPlatformTenantSelfWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):
        kind (ListPlatformTenantSelfWorkflowPerformanceInstancesKind | Unset):
        decision_code (str | Unset):
        rule_id (str | Unset):
        rule_version (str | Unset):
        group_id (str | Unset):
        path_id (str | Unset):
        condition_id (str | Unset):
        condition_description (str | Unset):
        condition_result (ListPlatformTenantSelfWorkflowPerformanceInstancesConditionResult |
            Unset):
        outcome_code (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPerformanceInstancesResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            workflow=workflow,
            cohort=cohort,
            dimension=dimension,
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
            condition_result=condition_result,
            outcome_code=outcome_code,
        )
    ).parsed
