from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_action_kind import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind,
)
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_action_trigger import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger,
)
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_due import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsDue,
)
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_impact_kind import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind,
)
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_impact_priority import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority,
)
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_sort import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsSort,
)
from ...models.list_platform_tenant_self_workflow_readiness_follow_ups_view import (
    ListPlatformTenantSelfWorkflowReadinessFollowUpsView,
)
from ...models.operation_workflow_readiness_follow_ups_response import OperationWorkflowReadinessFollowUpsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    view: ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    app_id: UUID,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_view: str | Unset = UNSET
    if not isinstance(view, Unset):
        json_view = view

    params["view"] = json_view

    json_action_kind: str | Unset = UNSET
    if not isinstance(action_kind, Unset):
        json_action_kind = action_kind

    params["action_kind"] = json_action_kind

    json_action_trigger: str | Unset = UNSET
    if not isinstance(action_trigger, Unset):
        json_action_trigger = action_trigger

    params["action_trigger"] = json_action_trigger

    params["scope"] = scope

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["owner"] = owner

    params["workflow"] = workflow

    json_due: str | Unset = UNSET
    if not isinstance(due, Unset):
        json_due = due

    params["due"] = json_due

    params["offset"] = offset

    params["limit"] = limit

    params["reminder_only"] = reminder_only

    json_impact_priority: str | Unset = UNSET
    if not isinstance(impact_priority, Unset):
        json_impact_priority = impact_priority

    params["impact_priority"] = json_impact_priority

    json_impact_kind: str | Unset = UNSET
    if not isinstance(impact_kind, Unset):
        json_impact_kind = impact_kind

    params["impact_kind"] = json_impact_kind

    params["impact_code"] = impact_code

    json_sort: str | Unset = UNSET
    if not isinstance(sort, Unset):
        json_sort = sort

    params["sort"] = json_sort

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-readiness/follow-ups",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessFollowUpsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    view: ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    app_id: UUID,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    """List PlatformTenantSelf readiness follow-up work queue

     ListPlatformTenantSelfWorkflowReadinessFollowUps. Read retained pending follow-ups across
    subscriptions for an explicit application and environment. Due-time ordering puts overdue first.
    Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer
    reads require Operations read.

    Args:
        view (ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        app_id (UUID):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessFollowUpsResponse | Problem]
    """

    kwargs = _get_kwargs(
        view=view,
        action_kind=action_kind,
        action_trigger=action_trigger,
        scope=scope,
        app_id=app_id,
        owner=owner,
        workflow=workflow,
        due=due,
        offset=offset,
        limit=limit,
        reminder_only=reminder_only,
        impact_priority=impact_priority,
        impact_kind=impact_kind,
        impact_code=impact_code,
        sort=sort,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    view: ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    app_id: UUID,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    """List PlatformTenantSelf readiness follow-up work queue

     ListPlatformTenantSelfWorkflowReadinessFollowUps. Read retained pending follow-ups across
    subscriptions for an explicit application and environment. Due-time ordering puts overdue first.
    Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer
    reads require Operations read.

    Args:
        view (ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        app_id (UUID):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessFollowUpsResponse | Problem
    """

    return sync_detailed(
        client=client,
        view=view,
        action_kind=action_kind,
        action_trigger=action_trigger,
        scope=scope,
        app_id=app_id,
        owner=owner,
        workflow=workflow,
        due=due,
        offset=offset,
        limit=limit,
        reminder_only=reminder_only,
        impact_priority=impact_priority,
        impact_kind=impact_kind,
        impact_code=impact_code,
        sort=sort,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    view: ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    app_id: UUID,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    """List PlatformTenantSelf readiness follow-up work queue

     ListPlatformTenantSelfWorkflowReadinessFollowUps. Read retained pending follow-ups across
    subscriptions for an explicit application and environment. Due-time ordering puts overdue first.
    Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer
    reads require Operations read.

    Args:
        view (ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        app_id (UUID):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessFollowUpsResponse | Problem]
    """

    kwargs = _get_kwargs(
        view=view,
        action_kind=action_kind,
        action_trigger=action_trigger,
        scope=scope,
        app_id=app_id,
        owner=owner,
        workflow=workflow,
        due=due,
        offset=offset,
        limit=limit,
        reminder_only=reminder_only,
        impact_priority=impact_priority,
        impact_kind=impact_kind,
        impact_code=impact_code,
        sort=sort,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    view: ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    app_id: UUID,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    """List PlatformTenantSelf readiness follow-up work queue

     ListPlatformTenantSelfWorkflowReadinessFollowUps. Read retained pending follow-ups across
    subscriptions for an explicit application and environment. Due-time ordering puts overdue first.
    Pages are live and may move; refresh after edits. Account reads require read scope and MFA; customer
    reads require Operations read.

    Args:
        view (ListPlatformTenantSelfWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListPlatformTenantSelfWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        app_id (UUID):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListPlatformTenantSelfWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListPlatformTenantSelfWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListPlatformTenantSelfWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessFollowUpsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            view=view,
            action_kind=action_kind,
            action_trigger=action_trigger,
            scope=scope,
            app_id=app_id,
            owner=owner,
            workflow=workflow,
            due=due,
            offset=offset,
            limit=limit,
            reminder_only=reminder_only,
            impact_priority=impact_priority,
            impact_kind=impact_kind,
            impact_code=impact_code,
            sort=sort,
        )
    ).parsed
