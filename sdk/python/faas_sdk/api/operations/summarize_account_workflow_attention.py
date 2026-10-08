from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_attention_summary import OperationWorkflowAttentionSummary
from ...models.problem import Problem
from ...models.summarize_account_workflow_attention_dependency_status import (
    SummarizeAccountWorkflowAttentionDependencyStatus,
)
from ...models.summarize_account_workflow_attention_group_by import (
    SummarizeAccountWorkflowAttentionGroupBy,
)
from ...models.summarize_account_workflow_attention_reason import (
    SummarizeAccountWorkflowAttentionReason,
)
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    group_by: SummarizeAccountWorkflowAttentionGroupBy | Unset = "workflow",
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: SummarizeAccountWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: SummarizeAccountWorkflowAttentionReason | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_group_by: str | Unset = UNSET
    if not isinstance(group_by, Unset):
        json_group_by = group_by

    params["group_by"] = json_group_by

    params["workflow"] = workflow

    params["target_operation"] = target_operation

    json_dependency_status: str | Unset = UNSET
    if not isinstance(dependency_status, Unset):
        json_dependency_status = dependency_status

    params["dependency_status"] = json_dependency_status

    params["required_outcome_code"] = required_outcome_code

    params["blocker_code"] = blocker_code

    json_reason: str | Unset = UNSET
    if not isinstance(reason, Unset):
        json_reason = reason

    params["reason"] = json_reason

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-attention/summary".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowAttentionSummary | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowAttentionSummary.from_dict(response.json())

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
) -> Response[OperationWorkflowAttentionSummary | Problem]:
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
    group_by: SummarizeAccountWorkflowAttentionGroupBy | Unset = "workflow",
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: SummarizeAccountWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: SummarizeAccountWorkflowAttentionReason | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowAttentionSummary | Problem]:
    """Summarize all matching retained workflow attention.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated
    at the first page time carried by the cursor; current reports and retention can change during
    browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or
    applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained
    snapshots regardless of group pagination. Groups may overlap and should not be added together.
    Blocker statistics within code and target groups cover that code or target only. Ages derive from
    application-reported first_observed_at; missing ages are counted separately. Group cursors are
    separate from queue cursors. The summary grants no execution authority.

    Args:
        slug (str):
        scope (str):
        group_by (SummarizeAccountWorkflowAttentionGroupBy | Unset):  Default: 'workflow'.
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (SummarizeAccountWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        blocker_code (str | Unset):
        reason (SummarizeAccountWorkflowAttentionReason | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowAttentionSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        group_by=group_by,
        workflow=workflow,
        target_operation=target_operation,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        reason=reason,
        tenant_id=tenant_id,
        limit=limit,
        cursor=cursor,
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
    group_by: SummarizeAccountWorkflowAttentionGroupBy | Unset = "workflow",
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: SummarizeAccountWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: SummarizeAccountWorkflowAttentionReason | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowAttentionSummary | Problem | None:
    """Summarize all matching retained workflow attention.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated
    at the first page time carried by the cursor; current reports and retention can change during
    browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or
    applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained
    snapshots regardless of group pagination. Groups may overlap and should not be added together.
    Blocker statistics within code and target groups cover that code or target only. Ages derive from
    application-reported first_observed_at; missing ages are counted separately. Group cursors are
    separate from queue cursors. The summary grants no execution authority.

    Args:
        slug (str):
        scope (str):
        group_by (SummarizeAccountWorkflowAttentionGroupBy | Unset):  Default: 'workflow'.
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (SummarizeAccountWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        blocker_code (str | Unset):
        reason (SummarizeAccountWorkflowAttentionReason | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowAttentionSummary | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        group_by=group_by,
        workflow=workflow,
        target_operation=target_operation,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        reason=reason,
        tenant_id=tenant_id,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    group_by: SummarizeAccountWorkflowAttentionGroupBy | Unset = "workflow",
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: SummarizeAccountWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: SummarizeAccountWorkflowAttentionReason | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowAttentionSummary | Problem]:
    """Summarize all matching retained workflow attention.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated
    at the first page time carried by the cursor; current reports and retention can change during
    browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or
    applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained
    snapshots regardless of group pagination. Groups may overlap and should not be added together.
    Blocker statistics within code and target groups cover that code or target only. Ages derive from
    application-reported first_observed_at; missing ages are counted separately. Group cursors are
    separate from queue cursors. The summary grants no execution authority.

    Args:
        slug (str):
        scope (str):
        group_by (SummarizeAccountWorkflowAttentionGroupBy | Unset):  Default: 'workflow'.
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (SummarizeAccountWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        blocker_code (str | Unset):
        reason (SummarizeAccountWorkflowAttentionReason | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowAttentionSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        group_by=group_by,
        workflow=workflow,
        target_operation=target_operation,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        reason=reason,
        tenant_id=tenant_id,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    group_by: SummarizeAccountWorkflowAttentionGroupBy | Unset = "workflow",
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    dependency_status: SummarizeAccountWorkflowAttentionDependencyStatus | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    reason: SummarizeAccountWorkflowAttentionReason | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowAttentionSummary | Problem | None:
    """Summarize all matching retained workflow attention.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment selectors are required. Staleness and overdue deadlines are evaluated
    at the first page time carried by the cursor; current reports and retention can change during
    browsing. Unknown/unreported state is not enumerated. Target Operations match reported blockers or
    applicable selected-contract edges on stale or overdue workflows. Totals cover all matching retained
    snapshots regardless of group pagination. Groups may overlap and should not be added together.
    Blocker statistics within code and target groups cover that code or target only. Ages derive from
    application-reported first_observed_at; missing ages are counted separately. Group cursors are
    separate from queue cursors. The summary grants no execution authority.

    Args:
        slug (str):
        scope (str):
        group_by (SummarizeAccountWorkflowAttentionGroupBy | Unset):  Default: 'workflow'.
        workflow (str | Unset):
        target_operation (str | Unset):
        dependency_status (SummarizeAccountWorkflowAttentionDependencyStatus | Unset):
        required_outcome_code (str | Unset):
        blocker_code (str | Unset):
        reason (SummarizeAccountWorkflowAttentionReason | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowAttentionSummary | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            group_by=group_by,
            workflow=workflow,
            target_operation=target_operation,
            dependency_status=dependency_status,
            required_outcome_code=required_outcome_code,
            blocker_code=blocker_code,
            reason=reason,
            tenant_id=tenant_id,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
