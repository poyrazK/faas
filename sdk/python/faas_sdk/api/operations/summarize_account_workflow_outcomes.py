from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_outcome_summary import OperationWorkflowOutcomeSummary
from ...models.problem import Problem
from ...models.summarize_account_workflow_outcomes_group_by import (
    SummarizeAccountWorkflowOutcomesGroupBy,
)
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    group_by: SummarizeAccountWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params["workflow"] = workflow

    params["code"] = code

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    json_group_by: str | Unset = UNSET
    if not isinstance(group_by, Unset):
        json_group_by = group_by

    params["group_by"] = json_group_by

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-outcomes/summary".format(
            slug=quote(str(slug), safe=""),
        ),
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
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    group_by: SummarizeAccountWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowOutcomeSummary | Problem]:
    """Summarize completed workflows with explicit business outcomes.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment are required. Only latest retained terminal snapshots with reported
    outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier
    outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals
    cover all matching instances independently of group pagination; cursors are separate from the
    attention queue. Reports and retention may change during browsing.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        tenant_id (UUID | Unset):
        group_by (SummarizeAccountWorkflowOutcomesGroupBy | Unset):  Default: 'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowOutcomeSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        workflow=workflow,
        code=code,
        tenant_id=tenant_id,
        group_by=group_by,
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
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    group_by: SummarizeAccountWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowOutcomeSummary | Problem | None:
    """Summarize completed workflows with explicit business outcomes.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment are required. Only latest retained terminal snapshots with reported
    outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier
    outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals
    cover all matching instances independently of group pagination; cursors are separate from the
    attention queue. Reports and retention may change during browsing.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        tenant_id (UUID | Unset):
        group_by (SummarizeAccountWorkflowOutcomesGroupBy | Unset):  Default: 'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowOutcomeSummary | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        workflow=workflow,
        code=code,
        tenant_id=tenant_id,
        group_by=group_by,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    group_by: SummarizeAccountWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationWorkflowOutcomeSummary | Problem]:
    """Summarize completed workflows with explicit business outcomes.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment are required. Only latest retained terminal snapshots with reported
    outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier
    outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals
    cover all matching instances independently of group pagination; cursors are separate from the
    attention queue. Reports and retention may change during browsing.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        tenant_id (UUID | Unset):
        group_by (SummarizeAccountWorkflowOutcomesGroupBy | Unset):  Default: 'outcome'.
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowOutcomeSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        workflow=workflow,
        code=code,
        tenant_id=tenant_id,
        group_by=group_by,
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
    workflow: str | Unset = UNSET,
    code: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    group_by: SummarizeAccountWorkflowOutcomesGroupBy | Unset = "outcome",
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationWorkflowOutcomeSummary | Problem | None:
    """Summarize completed workflows with explicit business outcomes.

     Requires account read scope and MFA. Optional tenant selection remains within account ownership.
    Explicit app and environment are required. Only latest retained terminal snapshots with reported
    outcomes are included; each instance counts once. Reopened instances leave these totals. Earlier
    outcomes remain in retained workflow history. Terminal state alone does not imply an outcome. Totals
    cover all matching instances independently of group pagination; cursors are separate from the
    attention queue. Reports and retention may change during browsing.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        code (str | Unset):
        tenant_id (UUID | Unset):
        group_by (SummarizeAccountWorkflowOutcomesGroupBy | Unset):  Default: 'outcome'.
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
            slug=slug,
            client=client,
            scope=scope,
            workflow=workflow,
            code=code,
            tenant_id=tenant_id,
            group_by=group_by,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
