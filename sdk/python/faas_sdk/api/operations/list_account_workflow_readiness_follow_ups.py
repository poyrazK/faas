from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_account_workflow_readiness_follow_ups_due import (
    ListAccountWorkflowReadinessFollowUpsDue,
)
from ...models.operation_workflow_readiness_follow_ups_response import OperationWorkflowReadinessFollowUpsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

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
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessFollowUpsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
        owner=owner,
        workflow=workflow,
        due=due,
        offset=offset,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessFollowUpsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        tenant_id=tenant_id,
        owner=owner,
        workflow=workflow,
        due=due,
        offset=offset,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[OperationWorkflowReadinessFollowUpsResponse | Problem]:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessFollowUpsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
        owner=owner,
        workflow=workflow,
        due=due,
        offset=offset,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    owner: str | Unset = UNSET,
    workflow: str | Unset = UNSET,
    due: ListAccountWorkflowReadinessFollowUpsDue | Unset = UNSET,
    offset: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> OperationWorkflowReadinessFollowUpsResponse | Problem | None:
    """List Account readiness follow-up work queue

     ListAccountWorkflowReadinessFollowUps. Read retained pending follow-ups across subscriptions for an
    explicit application and environment. Due-time ordering puts overdue first. Pages are live and may
    move; refresh after edits. Account reads require read scope and MFA; customer reads require
    Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        owner (str | Unset):
        workflow (str | Unset):
        due (ListAccountWorkflowReadinessFollowUpsDue | Unset):
        offset (int | Unset):
        limit (int | Unset):

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
            scope=scope,
            tenant_id=tenant_id,
            owner=owner,
            workflow=workflow,
            due=due,
            offset=offset,
            limit=limit,
        )
    ).parsed
