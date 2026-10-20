from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_playbook_evidence_watch import OperationWorkflowPlaybookEvidenceWatch
from ...models.operation_workflow_playbook_evidence_watch_request import OperationWorkflowPlaybookEvidenceWatchRequest
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationWorkflowPlaybookEvidenceWatchRequest,
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
        "url": "/v1/platform-tenant-self/workflow-readiness/playbook-progress/{id}/evidence-watch".format(
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
) -> OperationWorkflowPlaybookEvidenceWatch | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowPlaybookEvidenceWatch.from_dict(response.json())

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
) -> Response[OperationWorkflowPlaybookEvidenceWatch | Problem]:
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
    body: OperationWorkflowPlaybookEvidenceWatchRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> Response[OperationWorkflowPlaybookEvidenceWatch | Problem]:
    """Configure customer checklist evidence monitoring

     ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned
    checklist in an existing readiness inbox. Requires the exact checklist revision and watch
    configuration revision (zero for a new watch). Enabling captures a baseline without delivering a
    notice. Status or reason changes subsequently publish one grouped notification; clocks and report
    revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are
    required. Closure and revision are rechecked under the shared writer lock before publication.
    Polling does not change checklist progress or business state. At most 20 retained watches per
    subscription; stopped entries may be reclaimed for new watches.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookEvidenceWatchRequest):
            OperationWorkflowPlaybookEvidenceWatchRequest. Opt-in durable checklist evidence
            monitoring; advisory observations preserve human reviews and application business
            authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPlaybookEvidenceWatch | Problem]
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
    body: OperationWorkflowPlaybookEvidenceWatchRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> OperationWorkflowPlaybookEvidenceWatch | Problem | None:
    """Configure customer checklist evidence monitoring

     ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned
    checklist in an existing readiness inbox. Requires the exact checklist revision and watch
    configuration revision (zero for a new watch). Enabling captures a baseline without delivering a
    notice. Status or reason changes subsequently publish one grouped notification; clocks and report
    revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are
    required. Closure and revision are rechecked under the shared writer lock before publication.
    Polling does not change checklist progress or business state. At most 20 retained watches per
    subscription; stopped entries may be reclaimed for new watches.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookEvidenceWatchRequest):
            OperationWorkflowPlaybookEvidenceWatchRequest. Opt-in durable checklist evidence
            monitoring; advisory observations preserve human reviews and application business
            authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPlaybookEvidenceWatch | Problem
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
    body: OperationWorkflowPlaybookEvidenceWatchRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> Response[OperationWorkflowPlaybookEvidenceWatch | Problem]:
    """Configure customer checklist evidence monitoring

     ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned
    checklist in an existing readiness inbox. Requires the exact checklist revision and watch
    configuration revision (zero for a new watch). Enabling captures a baseline without delivering a
    notice. Status or reason changes subsequently publish one grouped notification; clocks and report
    revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are
    required. Closure and revision are rechecked under the shared writer lock before publication.
    Polling does not change checklist progress or business state. At most 20 retained watches per
    subscription; stopped entries may be reclaimed for new watches.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookEvidenceWatchRequest):
            OperationWorkflowPlaybookEvidenceWatchRequest. Opt-in durable checklist evidence
            monitoring; advisory observations preserve human reviews and application business
            authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPlaybookEvidenceWatch | Problem]
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
    body: OperationWorkflowPlaybookEvidenceWatchRequest,
    app_id: UUID,
    scope: str,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
) -> OperationWorkflowPlaybookEvidenceWatch | Problem | None:
    """Configure customer checklist evidence monitoring

     ConfigurePlatformTenantSelfWorkflowPlaybookEvidenceWatch. Explicitly enroll or stop a pinned
    checklist in an existing readiness inbox. Requires the exact checklist revision and watch
    configuration revision (zero for a new watch). Enabling captures a baseline without delivering a
    notice. Status or reason changes subsequently publish one grouped notification; clocks and report
    revisions alone do not notify. Account write scope and MFA or tenant operations-manage scope are
    required. Closure and revision are rechecked under the shared writer lock before publication.
    Polling does not change checklist progress or business state. At most 20 retained watches per
    subscription; stopped entries may be reclaimed for new watches.

    Args:
        id (UUID):
        app_id (UUID):
        scope (str):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        body (OperationWorkflowPlaybookEvidenceWatchRequest):
            OperationWorkflowPlaybookEvidenceWatchRequest. Opt-in durable checklist evidence
            monitoring; advisory observations preserve human reviews and application business
            authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPlaybookEvidenceWatch | Problem
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
