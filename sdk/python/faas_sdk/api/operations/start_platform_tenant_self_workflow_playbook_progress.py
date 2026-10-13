from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_playbook_progress import OperationWorkflowPlaybookProgress
from ...models.operation_workflow_playbook_progress_start_request import OperationWorkflowPlaybookProgressStartRequest
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    *,
    body: OperationWorkflowPlaybookProgressStartRequest,
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
        "method": "post",
        "url": "/v1/platform-tenant-self/workflow-readiness/playbook-progress",
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
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressStartRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> Response[OperationWorkflowPlaybookProgress | Problem]:
    """Start customer pinned workflow playbook progress

     StartPlatformTenantSelfWorkflowPlaybookProgress. Start from a currently matched fresh application
    playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts
    return the existing checklist. Requires account write scope and MFA or customer operations-manage
    scope. Customer identity derives from authentication. Checklist completion does not establish
    readiness, authorize execution or report business success. At most 100 records per exact case; the
    latest 64 review changes per record are retained.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressStartRequest):
            OperationWorkflowPlaybookProgressStartRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPlaybookProgress | Problem]
    """

    kwargs = _get_kwargs(
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
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressStartRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> OperationWorkflowPlaybookProgress | Problem | None:
    """Start customer pinned workflow playbook progress

     StartPlatformTenantSelfWorkflowPlaybookProgress. Start from a currently matched fresh application
    playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts
    return the existing checklist. Requires account write scope and MFA or customer operations-manage
    scope. Customer identity derives from authentication. Checklist completion does not establish
    readiness, authorize execution or report business success. At most 100 records per exact case; the
    latest 64 review changes per record are retained.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressStartRequest):
            OperationWorkflowPlaybookProgressStartRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPlaybookProgress | Problem
    """

    return sync_detailed(
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
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressStartRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> Response[OperationWorkflowPlaybookProgress | Problem]:
    """Start customer pinned workflow playbook progress

     StartPlatformTenantSelfWorkflowPlaybookProgress. Start from a currently matched fresh application
    playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts
    return the existing checklist. Requires account write scope and MFA or customer operations-manage
    scope. Customer identity derives from authentication. Checklist completion does not establish
    readiness, authorize execution or report business success. At most 100 records per exact case; the
    latest 64 review changes per record are retained.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressStartRequest):
            OperationWorkflowPlaybookProgressStartRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPlaybookProgress | Problem]
    """

    kwargs = _get_kwargs(
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
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowPlaybookProgressStartRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> OperationWorkflowPlaybookProgress | Problem | None:
    """Start customer pinned workflow playbook progress

     StartPlatformTenantSelfWorkflowPlaybookProgress. Start from a currently matched fresh application
    playbook. Pin exact rule, workflow version and immutable reporting definition. Repeated starts
    return the existing checklist. Requires account write scope and MFA or customer operations-manage
    scope. Customer identity derives from authentication. Checklist completion does not establish
    readiness, authorize execution or report business success. At most 100 records per exact case; the
    latest 64 review changes per record are retained.

    Args:
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookProgressStartRequest):
            OperationWorkflowPlaybookProgressStartRequest. Durable advisory checklist coordination,
            independent of application business success.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPlaybookProgress | Problem
    """

    return (
        await asyncio_detailed(
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
