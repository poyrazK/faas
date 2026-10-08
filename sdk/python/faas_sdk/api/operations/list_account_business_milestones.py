from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_milestones_response import OperationMilestonesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    params["workflow"] = workflow

    params["workflow_instance_id"] = workflow_instance_id

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params["limit"] = limit

    params["cursor"] = cursor

    params["workflow_state_cursor"] = workflow_state_cursor

    params["stale_only"] = stale_only

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operation-milestones".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationMilestonesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationMilestonesResponse.from_dict(response.json())

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
) -> Response[OperationMilestonesResponse | Problem]:
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
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> Response[OperationMilestonesResponse | Problem]:
    """Read a business entity timeline across related Operations.

     Requires account read scope and MFA. Explicit app/environment/reference selectors and optional
    customer selection remain within account ownership. Paired workflow and workflow-instance selectors
    narrow the feed to one run. Facts are ordered by first platform publication time, not inferred
    business causality.

    Args:
        slug (str):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        workflow_state_cursor (str | Unset):
        stale_only (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationMilestonesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        workflow_instance_id=workflow_instance_id,
        tenant_id=tenant_id,
        limit=limit,
        cursor=cursor,
        workflow_state_cursor=workflow_state_cursor,
        stale_only=stale_only,
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
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> OperationMilestonesResponse | Problem | None:
    """Read a business entity timeline across related Operations.

     Requires account read scope and MFA. Explicit app/environment/reference selectors and optional
    customer selection remain within account ownership. Paired workflow and workflow-instance selectors
    narrow the feed to one run. Facts are ordered by first platform publication time, not inferred
    business causality.

    Args:
        slug (str):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        workflow_state_cursor (str | Unset):
        stale_only (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationMilestonesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        workflow_instance_id=workflow_instance_id,
        tenant_id=tenant_id,
        limit=limit,
        cursor=cursor,
        workflow_state_cursor=workflow_state_cursor,
        stale_only=stale_only,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> Response[OperationMilestonesResponse | Problem]:
    """Read a business entity timeline across related Operations.

     Requires account read scope and MFA. Explicit app/environment/reference selectors and optional
    customer selection remain within account ownership. Paired workflow and workflow-instance selectors
    narrow the feed to one run. Facts are ordered by first platform publication time, not inferred
    business causality.

    Args:
        slug (str):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        workflow_state_cursor (str | Unset):
        stale_only (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationMilestonesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        workflow_instance_id=workflow_instance_id,
        tenant_id=tenant_id,
        limit=limit,
        cursor=cursor,
        workflow_state_cursor=workflow_state_cursor,
        stale_only=stale_only,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    tenant_id: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> OperationMilestonesResponse | Problem | None:
    """Read a business entity timeline across related Operations.

     Requires account read scope and MFA. Explicit app/environment/reference selectors and optional
    customer selection remain within account ownership. Paired workflow and workflow-instance selectors
    narrow the feed to one run. Facts are ordered by first platform publication time, not inferred
    business causality.

    Args:
        slug (str):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
        tenant_id (UUID | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        workflow_state_cursor (str | Unset):
        stale_only (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationMilestonesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            workflow_instance_id=workflow_instance_id,
            tenant_id=tenant_id,
            limit=limit,
            cursor=cursor,
            workflow_state_cursor=workflow_state_cursor,
            stale_only=stale_only,
        )
    ).parsed
