from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_outcome_summary import OperationWorkflowOutcomeSummary
from ...models.problem import Problem
from ...models.summarize_platform_tenant_self_workflow_outcomes_group_by import (
    SummarizePlatformTenantSelfWorkflowOutcomesGroupBy,
)
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    group_by: SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["workflow"] = workflow

    params["code"] = code

    json_group_by: str | Unset = UNSET
    if not isinstance(group_by, Unset):
        json_group_by = group_by

    params["group_by"] = json_group_by

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-outcomes/summary",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowOutcomeSummary | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowOutcomeSummary.from_dict(response.json())

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
) -> Response[OperationWorkflowOutcomeSummary | Problem]:
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
    code: str | Unset = UNSET,
    group_by: SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowOutcomeSummary | Problem]:
    """Summarize completed workflows with explicit business outcomes.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app and environment are required. Only latest retained terminal snapshots
    with reported outcomes are included; each instance counts once. Reopened instances leave these
    totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an
    outcome. Totals cover all matching instances independently of group pagination; cursors are separate
    from the attention queue. Reports and retention may change during browsing.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        group_by (SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset):  Default:
            'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowOutcomeSummary | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        code=code,
        group_by=group_by,
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
    code: str | Unset = UNSET,
    group_by: SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowOutcomeSummary | Problem | None:
    """Summarize completed workflows with explicit business outcomes.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app and environment are required. Only latest retained terminal snapshots
    with reported outcomes are included; each instance counts once. Reopened instances leave these
    totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an
    outcome. Totals cover all matching instances independently of group pagination; cursors are separate
    from the attention queue. Reports and retention may change during browsing.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        group_by (SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset):  Default:
            'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowOutcomeSummary | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        code=code,
        group_by=group_by,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    group_by: SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowOutcomeSummary | Problem]:
    """Summarize completed workflows with explicit business outcomes.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app and environment are required. Only latest retained terminal snapshots
    with reported outcomes are included; each instance counts once. Reopened instances leave these
    totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an
    outcome. Totals cover all matching instances independently of group pagination; cursors are separate
    from the attention queue. Reports and retention may change during browsing.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        group_by (SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset):  Default:
            'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowOutcomeSummary | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
        code=code,
        group_by=group_by,
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
    code: str | Unset = UNSET,
    group_by: SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowOutcomeSummary | Problem | None:
    """Summarize completed workflows with explicit business outcomes.

     Requires platform_tenant:operations:read. Customer identity comes from credentials; tenant overrides
    are rejected. Explicit app and environment are required. Only latest retained terminal snapshots
    with reported outcomes are included; each instance counts once. Reopened instances leave these
    totals. Earlier outcomes remain in retained workflow history. Terminal state alone does not imply an
    outcome. Totals cover all matching instances independently of group pagination; cursors are separate
    from the attention queue. Reports and retention may change during browsing.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        group_by (SummarizePlatformTenantSelfWorkflowOutcomesGroupBy | Unset):  Default:
            'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowOutcomeSummary | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            workflow=workflow,
            code=code,
            group_by=group_by,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
