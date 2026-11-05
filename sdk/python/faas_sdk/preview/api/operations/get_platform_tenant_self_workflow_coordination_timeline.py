from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_coordination_timeline import OperationWorkflowCoordinationTimeline
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    params["workflow"] = workflow

    params["instance_id"] = instance_id

    params["offset"] = offset

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-readiness/timeline",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowCoordinationTimeline | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowCoordinationTimeline.from_dict(response.json())

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
) -> Response[OperationWorkflowCoordinationTimeline | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> Response[OperationWorkflowCoordinationTimeline | Problem]:
    """Read customer workflow coordination timeline

     GetPlatformTenantSelfWorkflowCoordinationTimeline. Read a bounded chronological projection from
    current retained readiness inboxes for an exact subject and workflow instance. Observation clocks
    are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit
    trail and does not authorize execution. Sources are read independently and live offset pages may
    move. Account access requires read scope and MFA; customer access requires Operations read.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowCoordinationTimeline | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
        offset=offset,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> OperationWorkflowCoordinationTimeline | Problem | None:
    """Read customer workflow coordination timeline

     GetPlatformTenantSelfWorkflowCoordinationTimeline. Read a bounded chronological projection from
    current retained readiness inboxes for an exact subject and workflow instance. Observation clocks
    are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit
    trail and does not authorize execution. Sources are read independently and live offset pages may
    move. Account access requires read scope and MFA; customer access requires Operations read.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowCoordinationTimeline | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
        offset=offset,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> Response[OperationWorkflowCoordinationTimeline | Problem]:
    """Read customer workflow coordination timeline

     GetPlatformTenantSelfWorkflowCoordinationTimeline. Read a bounded chronological projection from
    current retained readiness inboxes for an exact subject and workflow instance. Observation clocks
    are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit
    trail and does not authorize execution. Sources are read independently and live offset pages may
    move. Account access requires read scope and MFA; customer access requires Operations read.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowCoordinationTimeline | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
        offset=offset,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> OperationWorkflowCoordinationTimeline | Problem | None:
    """Read customer workflow coordination timeline

     GetPlatformTenantSelfWorkflowCoordinationTimeline. Read a bounded chronological projection from
    current retained readiness inboxes for an exact subject and workflow instance. Observation clocks
    are server times; latest snapshots do not reconstruct every past edit. This is not a complete audit
    trail and does not authorize execution. Sources are read independently and live offset pages may
    move. Account access requires read scope and MFA; customer access requires Operations read.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowCoordinationTimeline | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            instance_id=instance_id,
            offset=offset,
            limit=limit,
        )
    ).parsed
