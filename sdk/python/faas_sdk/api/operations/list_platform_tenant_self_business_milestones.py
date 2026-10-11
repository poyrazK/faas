from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_milestones_response import OperationMilestonesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    params["workflow"] = workflow

    params["workflow_instance_id"] = workflow_instance_id

    params["limit"] = limit

    params["cursor"] = cursor

    params["workflow_state_cursor"] = workflow_state_cursor

    params["stale_only"] = stale_only

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/customer-operation-milestones",
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
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> Response[OperationMilestonesResponse | Problem]:
    """Read a business entity timeline for the authenticated customer.

     Requires platform_tenant:operations:read. Identity comes only from credentials. The app,
    environment, and paired public reference select related retained work. Paired workflow and workflow-
    instance selectors narrow the feed to one run and include a grouped workflow_instance view with
    ordered declared steps, latest matching facts, current state, and independently paginated transition
    history. Opaque pagination binds all selectors; other customers with the same entity ID remain
    isolated.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
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
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        workflow_instance_id=workflow_instance_id,
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
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> OperationMilestonesResponse | Problem | None:
    """Read a business entity timeline for the authenticated customer.

     Requires platform_tenant:operations:read. Identity comes only from credentials. The app,
    environment, and paired public reference select related retained work. Paired workflow and workflow-
    instance selectors narrow the feed to one run and include a grouped workflow_instance view with
    ordered declared steps, latest matching facts, current state, and independently paginated transition
    history. Opaque pagination binds all selectors; other customers with the same entity ID remain
    isolated.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
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
        client=client,
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        workflow_instance_id=workflow_instance_id,
        limit=limit,
        cursor=cursor,
        workflow_state_cursor=workflow_state_cursor,
        stale_only=stale_only,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> Response[OperationMilestonesResponse | Problem]:
    """Read a business entity timeline for the authenticated customer.

     Requires platform_tenant:operations:read. Identity comes only from credentials. The app,
    environment, and paired public reference select related retained work. Paired workflow and workflow-
    instance selectors narrow the feed to one run and include a grouped workflow_instance view with
    ordered declared steps, latest matching facts, current state, and independently paginated transition
    history. Opaque pagination binds all selectors; other customers with the same entity ID remain
    isolated.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
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
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        workflow_instance_id=workflow_instance_id,
        limit=limit,
        cursor=cursor,
        workflow_state_cursor=workflow_state_cursor,
        stale_only=stale_only,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str | Unset = UNSET,
    workflow_instance_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    workflow_state_cursor: str | Unset = UNSET,
    stale_only: bool | Unset = False,
) -> OperationMilestonesResponse | Problem | None:
    """Read a business entity timeline for the authenticated customer.

     Requires platform_tenant:operations:read. Identity comes only from credentials. The app,
    environment, and paired public reference select related retained work. Paired workflow and workflow-
    instance selectors narrow the feed to one run and include a grouped workflow_instance view with
    ordered declared steps, latest matching facts, current state, and independently paginated transition
    history. Opaque pagination binds all selectors; other customers with the same entity ID remain
    isolated.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str | Unset):
        workflow_instance_id (str | Unset):
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
            client=client,
            app_id=app_id,
            scope=scope,
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            workflow_instance_id=workflow_instance_id,
            limit=limit,
            cursor=cursor,
            workflow_state_cursor=workflow_state_cursor,
            stale_only=stale_only,
        )
    ).parsed
