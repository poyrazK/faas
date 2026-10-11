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
from ...models.list_account_workflow_performance_instances_dimension import (
    ListAccountWorkflowPerformanceInstancesDimension,
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
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
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

    params["contract_version"] = contract_version

    params["state"] = state

    params["operation"] = operation

    params["code"] = code

    params["owner"] = owner

    params["unassigned"] = unassigned

    params["cohort_token"] = cohort_token

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
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
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
    pending verification previews are bounded independently of exact counts.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):

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
        contract_version=contract_version,
        state=state,
        operation=operation,
        code=code,
        owner=owner,
        unassigned=unassigned,
        cohort_token=cohort_token,
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
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
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
    pending verification previews are bounded independently of exact counts.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):

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
        contract_version=contract_version,
        state=state,
        operation=operation,
        code=code,
        owner=owner,
        unassigned=unassigned,
        cohort_token=cohort_token,
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
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
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
    pending verification previews are bounded independently of exact counts.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):

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
        contract_version=contract_version,
        state=state,
        operation=operation,
        code=code,
        owner=owner,
        unassigned=unassigned,
        cohort_token=cohort_token,
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
    contract_version: int | Unset = UNSET,
    state: str | Unset = UNSET,
    operation: str | Unset = UNSET,
    code: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    cohort_token: str | Unset = UNSET,
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
    pending verification previews are bounded independently of exact counts.

    Args:
        slug (str):
        scope (str):
        workflow (str):
        tenant_id (UUID | Unset):
        cohort (ListAccountWorkflowPerformanceInstancesCohort):
        dimension (ListAccountWorkflowPerformanceInstancesDimension):
        contract_version (int | Unset):
        state (str | Unset):
        operation (str | Unset):
        code (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        cohort_token (str | Unset):

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
            contract_version=contract_version,
            state=state,
            operation=operation,
            code=code,
            owner=owner,
            unassigned=unassigned,
            cohort_token=cohort_token,
        )
    ).parsed
