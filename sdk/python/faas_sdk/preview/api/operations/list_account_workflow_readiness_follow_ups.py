from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_account_workflow_readiness_follow_ups_action_kind import (
    ListAccountWorkflowReadinessFollowUpsActionKind,
)
from ...models.list_account_workflow_readiness_follow_ups_action_trigger import (
    ListAccountWorkflowReadinessFollowUpsActionTrigger,
)
from ...models.list_account_workflow_readiness_follow_ups_due import (
    ListAccountWorkflowReadinessFollowUpsDue,
)
from ...models.list_account_workflow_readiness_follow_ups_impact_kind import (
    ListAccountWorkflowReadinessFollowUpsImpactKind,
)
from ...models.list_account_workflow_readiness_follow_ups_impact_priority import (
    ListAccountWorkflowReadinessFollowUpsImpactPriority,
)
from ...models.list_account_workflow_readiness_follow_ups_sort import (
    ListAccountWorkflowReadinessFollowUpsSort,
)
from ...models.list_account_workflow_readiness_follow_ups_view import (
    ListAccountWorkflowReadinessFollowUpsView,
)
from ...models.operation_workflow_readiness_follow_ups_response import OperationWorkflowReadinessFollowUpsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    view: ListAccountWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListAccountWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListAccountWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListAccountWorkflowReadinessFollowUpsSort | Unset = UNSET,
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

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

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
        "url": "/v1/apps/{slug}/workflow-readiness/follow-ups".format(
            slug=quote(str(slug), safe=""),
        ),
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
    slug: str,
    *,
    client: AuthenticatedClient,
    view: ListAccountWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListAccountWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListAccountWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListAccountWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        view (ListAccountWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListAccountWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListAccountWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListAccountWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessFollowUpsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        view=view,
        action_kind=action_kind,
        action_trigger=action_trigger,
        scope=scope,
        tenant_id=tenant_id,
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
    slug: str,
    *,
    client: AuthenticatedClient,
    view: ListAccountWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListAccountWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListAccountWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListAccountWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        view (ListAccountWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListAccountWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListAccountWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListAccountWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessFollowUpsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        view=view,
        action_kind=action_kind,
        action_trigger=action_trigger,
        scope=scope,
        tenant_id=tenant_id,
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
    slug: str,
    *,
    client: AuthenticatedClient,
    view: ListAccountWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListAccountWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListAccountWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListAccountWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        view (ListAccountWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListAccountWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListAccountWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListAccountWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessFollowUpsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        view=view,
        action_kind=action_kind,
        action_trigger=action_trigger,
        scope=scope,
        tenant_id=tenant_id,
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
    slug: str,
    *,
    client: AuthenticatedClient,
    view: ListAccountWorkflowReadinessFollowUpsView | Unset = UNSET,
    action_kind: ListAccountWorkflowReadinessFollowUpsActionKind | Unset = UNSET,
    action_trigger: ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset = UNSET,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    reminder_only: bool | Unset = UNSET,
    impact_priority: ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset = UNSET,
    impact_kind: ListAccountWorkflowReadinessFollowUpsImpactKind | Unset = UNSET,
    impact_code: str | Unset = UNSET,
    sort: ListAccountWorkflowReadinessFollowUpsSort | Unset = UNSET,
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        view (ListAccountWorkflowReadinessFollowUpsView | Unset):
        action_kind (ListAccountWorkflowReadinessFollowUpsActionKind | Unset):
        action_trigger (ListAccountWorkflowReadinessFollowUpsActionTrigger | Unset):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):
        reminder_only (bool | Unset):
        impact_priority (ListAccountWorkflowReadinessFollowUpsImpactPriority | Unset):
        impact_kind (ListAccountWorkflowReadinessFollowUpsImpactKind | Unset):
        impact_code (str | Unset):
        sort (ListAccountWorkflowReadinessFollowUpsSort | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessFollowUpsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            view=view,
            action_kind=action_kind,
            action_trigger=action_trigger,
            scope=scope,
            tenant_id=tenant_id,
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
