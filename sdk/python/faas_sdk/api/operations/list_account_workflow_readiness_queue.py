from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_account_workflow_readiness_queue_requirement import (
    ListAccountWorkflowReadinessQueueRequirement,
)
from ...models.operation_workflow_readiness_queue_response import OperationWorkflowReadinessQueueResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    requirement: ListAccountWorkflowReadinessQueueRequirement | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params["workflow"] = workflow

    params["target_operation"] = target_operation

    params["owner"] = owner

    params["unassigned"] = unassigned

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    json_requirement: str | Unset = UNSET
    if not isinstance(requirement, Unset):
        json_requirement = requirement

    params["requirement"] = json_requirement

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-readiness/queue".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessQueueResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessQueueResponse.from_dict(response.json())

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
) -> Response[OperationWorkflowReadinessQueueResponse | Problem]:
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
    target_operation: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    requirement: ListAccountWorkflowReadinessQueueRequirement | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[OperationWorkflowReadinessQueueResponse | Problem]:
    """List account transition readiness across ongoing business workflows.

     Requires account read scope and MFA. Optional customer selection stays within account ownership.
    Explicit app and environment required. Samples the 100 most recently updated retained ongoing
    workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted
    current-state transitions per workflow with no planned evidence. Target Operation filters before
    edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner
    means an application-assigned blocker owner or pending verification owner for the target Operation;
    all retained pending obligations are considered before preview limits. Unassigned means one such
    finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness.
    Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order.
    Sampling and row truncation are reported separately; counts cover evaluated transitions, not an
    account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are
    excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate,
    empty, and unsupported query parameters are rejected.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        tenant_id (UUID | Unset):
        requirement (ListAccountWorkflowReadinessQueueRequirement | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessQueueResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        workflow=workflow,
        target_operation=target_operation,
        owner=owner,
        unassigned=unassigned,
        tenant_id=tenant_id,
        requirement=requirement,
        limit=limit,
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
    target_operation: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    requirement: ListAccountWorkflowReadinessQueueRequirement | Unset = UNSET,
    limit: int | Unset = 20,
) -> OperationWorkflowReadinessQueueResponse | Problem | None:
    """List account transition readiness across ongoing business workflows.

     Requires account read scope and MFA. Optional customer selection stays within account ownership.
    Explicit app and environment required. Samples the 100 most recently updated retained ongoing
    workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted
    current-state transitions per workflow with no planned evidence. Target Operation filters before
    edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner
    means an application-assigned blocker owner or pending verification owner for the target Operation;
    all retained pending obligations are considered before preview limits. Unassigned means one such
    finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness.
    Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order.
    Sampling and row truncation are reported separately; counts cover evaluated transitions, not an
    account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are
    excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate,
    empty, and unsupported query parameters are rejected.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        tenant_id (UUID | Unset):
        requirement (ListAccountWorkflowReadinessQueueRequirement | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessQueueResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        workflow=workflow,
        target_operation=target_operation,
        owner=owner,
        unassigned=unassigned,
        tenant_id=tenant_id,
        requirement=requirement,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    requirement: ListAccountWorkflowReadinessQueueRequirement | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[OperationWorkflowReadinessQueueResponse | Problem]:
    """List account transition readiness across ongoing business workflows.

     Requires account read scope and MFA. Optional customer selection stays within account ownership.
    Explicit app and environment required. Samples the 100 most recently updated retained ongoing
    workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted
    current-state transitions per workflow with no planned evidence. Target Operation filters before
    edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner
    means an application-assigned blocker owner or pending verification owner for the target Operation;
    all retained pending obligations are considered before preview limits. Unassigned means one such
    finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness.
    Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order.
    Sampling and row truncation are reported separately; counts cover evaluated transitions, not an
    account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are
    excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate,
    empty, and unsupported query parameters are rejected.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        tenant_id (UUID | Unset):
        requirement (ListAccountWorkflowReadinessQueueRequirement | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessQueueResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        workflow=workflow,
        target_operation=target_operation,
        owner=owner,
        unassigned=unassigned,
        tenant_id=tenant_id,
        requirement=requirement,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    workflow: str | Unset = UNSET,
    target_operation: str | Unset = UNSET,
    owner: str | Unset = UNSET,
    unassigned: bool | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    requirement: ListAccountWorkflowReadinessQueueRequirement | Unset = UNSET,
    limit: int | Unset = 20,
) -> OperationWorkflowReadinessQueueResponse | Problem | None:
    """List account transition readiness across ongoing business workflows.

     Requires account read scope and MFA. Optional customer selection stays within account ownership.
    Explicit app and environment required. Samples the 100 most recently updated retained ongoing
    workflows, ordered by update time then stable identity descending. Evaluates up to 100 sorted
    current-state transitions per workflow with no planned evidence. Target Operation filters before
    edge sampling; owner and requirement filters apply after evaluation but before row limit. Owner
    means an application-assigned blocker owner or pending verification owner for the target Operation;
    all retained pending obligations are considered before preview limits. Unassigned means one such
    finding has no owner. Reporting advisories never satisfy the requirement filter or change readiness.
    Unmet rows precede ready rows, preserving workflow sample order and Operation/from/to edge order.
    Sampling and row truncation are reported separately; counts cover evaluated transitions, not an
    account-wide backlog. Reads are live and not an atomic snapshot; changed workflow identities are
    excluded. No execution authority is granted. Unknown or terminal state is not enumerated. Duplicate,
    empty, and unsupported query parameters are rejected.

    Args:
        slug (str):
        scope (str):
        workflow (str | Unset):
        target_operation (str | Unset):
        owner (str | Unset):
        unassigned (bool | Unset):
        tenant_id (UUID | Unset):
        requirement (ListAccountWorkflowReadinessQueueRequirement | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessQueueResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            workflow=workflow,
            target_operation=target_operation,
            owner=owner,
            unassigned=unassigned,
            tenant_id=tenant_id,
            requirement=requirement,
            limit=limit,
        )
    ).parsed
