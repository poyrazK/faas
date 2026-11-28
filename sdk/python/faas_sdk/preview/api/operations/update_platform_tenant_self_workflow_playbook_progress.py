from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_playbook_progress import OperationWorkflowPlaybookProgress
from ...models.operation_workflow_playbook_progress_update_request import OperationWorkflowPlaybookProgressUpdateRequest
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationWorkflowPlaybookProgressUpdateRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    params["workflow"] = workflow

    params["instance_id"] = instance_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/platform-tenant-self/workflow-readiness/playbook-progress/{id}".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowPlaybookProgress | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowPlaybookProgress.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[OperationWorkflowPlaybookProgress | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressUpdateRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> Response[OperationWorkflowPlaybookProgress | Problem]:
    """Update customer pinned workflow playbook progress

     UpdatePlatformTenantSelfWorkflowPlaybookProgress. Replace one frozen checklist item using the
    expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence.
    Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or
    customer operations-manage scope. Customer identity derives from authentication. Checklist
    completion does not establish readiness, authorize execution or report business success. At most 100
    records per exact case; the latest 64 review changes per record are retained.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressUpdateRequest):
            OperationWorkflowPlaybookProgressUpdateRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPlaybookProgress | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressUpdateRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> OperationWorkflowPlaybookProgress | Problem | None:
    """Update customer pinned workflow playbook progress

     UpdatePlatformTenantSelfWorkflowPlaybookProgress. Replace one frozen checklist item using the
    expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence.
    Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or
    customer operations-manage scope. Customer identity derives from authentication. Checklist
    completion does not establish readiness, authorize execution or report business success. At most 100
    records per exact case; the latest 64 review changes per record are retained.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressUpdateRequest):
            OperationWorkflowPlaybookProgressUpdateRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPlaybookProgress | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressUpdateRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> Response[OperationWorkflowPlaybookProgress | Problem]:
    """Update customer pinned workflow playbook progress

     UpdatePlatformTenantSelfWorkflowPlaybookProgress. Replace one frozen checklist item using the
    expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence.
    Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or
    customer operations-manage scope. Customer identity derives from authentication. Checklist
    completion does not establish readiness, authorize execution or report business success. At most 100
    records per exact case; the latest 64 review changes per record are retained.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressUpdateRequest):
            OperationWorkflowPlaybookProgressUpdateRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPlaybookProgress | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        app_id=app_id,
        scope=scope,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressUpdateRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> OperationWorkflowPlaybookProgress | Problem | None:
    """Update customer pinned workflow playbook progress

     UpdatePlatformTenantSelfWorkflowPlaybookProgress. Replace one frozen checklist item using the
    expected revision. Record authenticated reviewer, note and scoped operation or milestone evidence.
    Conflicts return 409. Existing guidance is never upgraded. Requires account write scope and MFA or
    customer operations-manage scope. Customer identity derives from authentication. Checklist
    completion does not establish readiness, authorize execution or report business success. At most 100
    records per exact case; the latest 64 review changes per record are retained.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressUpdateRequest):
            OperationWorkflowPlaybookProgressUpdateRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPlaybookProgress | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            app_id=app_id,
            scope=scope,
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            instance_id=instance_id,
        )
    ).parsed
