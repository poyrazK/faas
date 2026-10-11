from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_platform_tenant_self_workflow_attention_dependency_status import (
    ListPlatformTenantSelfWorkflowAttentionDependencyStatus,
)
from ...models.list_platform_tenant_self_workflow_attention_priority import (
    ListPlatformTenantSelfWorkflowAttentionPriority,
)
from ...models.list_platform_tenant_self_workflow_attention_reason import (
    ListPlatformTenantSelfWorkflowAttentionReason,
)
from ...models.list_platform_tenant_self_workflow_attention_sort import (
    ListPlatformTenantSelfWorkflowAttentionSort,
)
from ...models.operation_workflow_attention_response import OperationWorkflowAttentionResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    priority: ListPlatformTenantSelfWorkflowAttentionPriority | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowAttentionSort | Unset = "updated_at",
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: ListPlatformTenantSelfWorkflowAttentionReason | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["workflow"] = workflow

    params["target_operation"] = target_operation

    json_dependency_status: str | Unset = UNSET
    if not isinstance(dependency_status, Unset):
        json_dependency_status = dependency_status

    params["dependency_status"] = json_dependency_status

    params["required_outcome_code"] = required_outcome_code

    json_priority: str | Unset = UNSET
    if not isinstance(priority, Unset):
        json_priority = priority

    params["priority"] = json_priority

    json_sort: str | Unset = UNSET
    if not isinstance(sort, Unset):
        json_sort = sort

    params["sort"] = json_sort

    params["owner"] = owner

    params["unassigned"] = unassigned

    params["blocker_code"] = blocker_code

    json_reason: str | Unset = UNSET
    if not isinstance(reason, Unset):
        json_reason = reason

    params["reason"] = json_reason

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-attention",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowAttentionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowAttentionResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowAttentionResponse | Problem]:
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
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    priority: ListPlatformTenantSelfWorkflowAttentionPriority | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowAttentionSort | Unset = "updated_at",
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: ListPlatformTenantSelfWorkflowAttentionReason | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowAttentionResponse | Problem]:
    """Customer attention queue — List retained blocked, stale, overdue, or dependency-waiting business
    workflow instances.

     Requires platform_tenant:operations:read. Customer identity comes only from credentials; tenant
    overrides are rejected. Explicit app and environment selectors are required. Staleness and overdue
    deadlines are evaluated at the first page time carried by the cursor; current reports and retention
    can change during browsing. Unknown/unreported state is not enumerated. Target Operations match
    reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue
    grants no execution authority.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        priority (ListPlatformTenantSelfWorkflowAttentionPriority | Unset):
        sort (ListPlatformTenantSelfWorkflowAttentionSort | Unset):  Default: 'updated_at'.
        owner (str | Unset):
        unassigned (bool | Unset):
        blocker_code (str | Unset):
        reason (ListPlatformTenantSelfWorkflowAttentionReason | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowAttentionResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        target_operation=target_operation,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        priority=priority,
        sort=sort,
        owner=owner,
        unassigned=unassigned,
        blocker_code=blocker_code,
        reason=reason,
        limit=limit,
        cursor=cursor,
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
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    priority: ListPlatformTenantSelfWorkflowAttentionPriority | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowAttentionSort | Unset = "updated_at",
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: ListPlatformTenantSelfWorkflowAttentionReason | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowAttentionResponse | Problem | None:
    """Customer attention queue — List retained blocked, stale, overdue, or dependency-waiting business
    workflow instances.

     Requires platform_tenant:operations:read. Customer identity comes only from credentials; tenant
    overrides are rejected. Explicit app and environment selectors are required. Staleness and overdue
    deadlines are evaluated at the first page time carried by the cursor; current reports and retention
    can change during browsing. Unknown/unreported state is not enumerated. Target Operations match
    reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue
    grants no execution authority.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        priority (ListPlatformTenantSelfWorkflowAttentionPriority | Unset):
        sort (ListPlatformTenantSelfWorkflowAttentionSort | Unset):  Default: 'updated_at'.
        owner (str | Unset):
        unassigned (bool | Unset):
        blocker_code (str | Unset):
        reason (ListPlatformTenantSelfWorkflowAttentionReason | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowAttentionResponse | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        target_operation=target_operation,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        priority=priority,
        sort=sort,
        owner=owner,
        unassigned=unassigned,
        blocker_code=blocker_code,
        reason=reason,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    priority: ListPlatformTenantSelfWorkflowAttentionPriority | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowAttentionSort | Unset = "updated_at",
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: ListPlatformTenantSelfWorkflowAttentionReason | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowAttentionResponse | Problem]:
    """Customer attention queue — List retained blocked, stale, overdue, or dependency-waiting business
    workflow instances.

     Requires platform_tenant:operations:read. Customer identity comes only from credentials; tenant
    overrides are rejected. Explicit app and environment selectors are required. Staleness and overdue
    deadlines are evaluated at the first page time carried by the cursor; current reports and retention
    can change during browsing. Unknown/unreported state is not enumerated. Target Operations match
    reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue
    grants no execution authority.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        priority (ListPlatformTenantSelfWorkflowAttentionPriority | Unset):
        sort (ListPlatformTenantSelfWorkflowAttentionSort | Unset):  Default: 'updated_at'.
        owner (str | Unset):
        unassigned (bool | Unset):
        blocker_code (str | Unset):
        reason (ListPlatformTenantSelfWorkflowAttentionReason | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowAttentionResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        target_operation=target_operation,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        priority=priority,
        sort=sort,
        owner=owner,
        unassigned=unassigned,
        blocker_code=blocker_code,
        reason=reason,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    priority: ListPlatformTenantSelfWorkflowAttentionPriority | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowAttentionSort | Unset = "updated_at",
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: ListPlatformTenantSelfWorkflowAttentionReason | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowAttentionResponse | Problem | None:
    """Customer attention queue — List retained blocked, stale, overdue, or dependency-waiting business
    workflow instances.

     Requires platform_tenant:operations:read. Customer identity comes only from credentials; tenant
    overrides are rejected. Explicit app and environment selectors are required. Staleness and overdue
    deadlines are evaluated at the first page time carried by the cursor; current reports and retention
    can change during browsing. Unknown/unreported state is not enumerated. Target Operations match
    reported blockers or applicable selected-contract edges on stale or overdue workflows. The queue
    grants no execution authority.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (ListPlatformTenantSelfWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        priority (ListPlatformTenantSelfWorkflowAttentionPriority | Unset):
        sort (ListPlatformTenantSelfWorkflowAttentionSort | Unset):  Default: 'updated_at'.
        owner (str | Unset):
        unassigned (bool | Unset):
        blocker_code (str | Unset):
        reason (ListPlatformTenantSelfWorkflowAttentionReason | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowAttentionResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            workflow=workflow,
            target_operation=target_operation,
            dependency_status=dependency_status,
            required_outcome_code=required_outcome_code,
            priority=priority,
            sort=sort,
            owner=owner,
            unassigned=unassigned,
            blocker_code=blocker_code,
            reason=reason,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
