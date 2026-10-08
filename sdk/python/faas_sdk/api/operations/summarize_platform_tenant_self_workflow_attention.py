from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_attention_summary import OperationWorkflowAttentionSummary
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    reason: str | Unset = UNSET,
    dependency_status: str | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    group_by: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["workflow"] = workflow

    params["limit"] = limit

    params["cursor"] = cursor

    params["target_operation"] = target_operation

    params["reason"] = reason
    params["dependency_status"] = dependency_status
    params["required_outcome_code"] = required_outcome_code
    params["blocker_code"] = blocker_code
    params["group_by"] = group_by

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-attention/summary",
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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    reason: str | Unset = UNSET,
    dependency_status: str | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    group_by: str | Unset = UNSET,
) -> Response[OperationWorkflowAttentionSummary | Problem]:
    """Summarize all matching retained blocked or stale workflows within the authenticated ownership boundary."""

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        limit=limit,
        cursor=cursor,
        target_operation=target_operation,
        reason=reason,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        group_by=group_by,
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
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    reason: str | Unset = UNSET,
    dependency_status: str | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    group_by: str | Unset = UNSET,
) -> OperationWorkflowAttentionSummary | Problem | None:
    """Summarize all matching retained blocked or stale workflows within the authenticated ownership boundary."""

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        limit=limit,
        cursor=cursor,
        target_operation=target_operation,
        reason=reason,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        group_by=group_by,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    reason: str | Unset = UNSET,
    dependency_status: str | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    group_by: str | Unset = UNSET,
) -> Response[OperationWorkflowAttentionSummary | Problem]:
    """Summarize all matching retained blocked or stale workflows within the authenticated ownership boundary."""

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        limit=limit,
        cursor=cursor,
        target_operation=target_operation,
        reason=reason,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        group_by=group_by,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    reason: str | Unset = UNSET,
    dependency_status: str | Unset = UNSET,
    required_outcome_code: str | Unset = UNSET,
    blocker_code: str | Unset = UNSET,
    group_by: str | Unset = UNSET,
) -> OperationWorkflowAttentionSummary | Problem | None:
    """Summarize all matching retained blocked or stale workflows within the authenticated ownership boundary."""

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            workflow=workflow,
            limit=limit,
            cursor=cursor,
            target_operation=target_operation,
            reason=reason,
        dependency_status=dependency_status,
        required_outcome_code=required_outcome_code,
        blocker_code=blocker_code,
        group_by=group_by,
        )
    ).parsed
