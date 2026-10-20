from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_artifact_response import OperationWorkflowArtifactResponse
from ...models.problem import Problem
from ...models.upload_workflow_operation_artifact_x_gregale_operation_execution_kind import (
    UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
)
from ...types import UNSET, File, Response


def _get_kwargs(
    id: UUID,
    *,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Gregale-Operation-Attempt"] = str(x_gregale_operation_attempt)

    headers["X-Gregale-Operation-Execution-Kind"] = str(x_gregale_operation_execution_kind)

    headers["X-Gregale-Operation-Workflow-Run-Id"] = str(x_gregale_operation_workflow_run_id)

    headers["X-Gregale-Operation-Workflow-Step"] = x_gregale_operation_workflow_step

    headers["X-Gregale-Operation-Generation"] = str(x_gregale_operation_generation)

    headers["X-Gregale-Operation-Workflow-Capability"] = str(x_gregale_operation_workflow_capability)

    params: dict[str, Any] = {}

    params["report_id"] = report_id

    params["name"] = name

    params["size_bytes"] = size_bytes

    params["sha256"] = sha256

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/workflow-operations/{id}/artifact-uploads".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["content"] = body.payload
    headers["Content-Type"] = "application/octet-stream"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowArtifactResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowArtifactResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[OperationWorkflowArtifactResponse | Problem]:
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
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> Response[OperationWorkflowArtifactResponse | Problem]:
    """Upload direct private result bytes with current final workflow action authority.

     Requires a current Operations workload assertion and native final-action proof for the pinned
    workflow run, step, generation and attempt. The descriptor declares immutable report ID, filename,
    byte count and SHA-256 without a source URI or storage credential. Each copy reserves unique staging
    storage and verifies exact bytes under existing quotas and transfer budgets. Current native owner,
    cancellation, lease and deadline are rechecked before private I/O and commit. Receipt identity
    remains stable across approved resumes; copies stay private until confirmed final-step success or
    explicit success recovery. Retention never settles a step or retries business effects.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (UploadWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_execution_kind=x_gregale_operation_execution_kind,
        x_gregale_operation_workflow_run_id=x_gregale_operation_workflow_run_id,
        x_gregale_operation_workflow_step=x_gregale_operation_workflow_step,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_workflow_capability=x_gregale_operation_workflow_capability,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> OperationWorkflowArtifactResponse | Problem | None:
    """Upload direct private result bytes with current final workflow action authority.

     Requires a current Operations workload assertion and native final-action proof for the pinned
    workflow run, step, generation and attempt. The descriptor declares immutable report ID, filename,
    byte count and SHA-256 without a source URI or storage credential. Each copy reserves unique staging
    storage and verifies exact bytes under existing quotas and transfer budgets. Current native owner,
    cancellation, lease and deadline are rechecked before private I/O and commit. Receipt identity
    remains stable across approved resumes; copies stay private until confirmed final-step success or
    explicit success recovery. Retention never settles a step or retries business effects.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (UploadWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowArtifactResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_execution_kind=x_gregale_operation_execution_kind,
        x_gregale_operation_workflow_run_id=x_gregale_operation_workflow_run_id,
        x_gregale_operation_workflow_step=x_gregale_operation_workflow_step,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_workflow_capability=x_gregale_operation_workflow_capability,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> Response[OperationWorkflowArtifactResponse | Problem]:
    """Upload direct private result bytes with current final workflow action authority.

     Requires a current Operations workload assertion and native final-action proof for the pinned
    workflow run, step, generation and attempt. The descriptor declares immutable report ID, filename,
    byte count and SHA-256 without a source URI or storage credential. Each copy reserves unique staging
    storage and verifies exact bytes under existing quotas and transfer budgets. Current native owner,
    cancellation, lease and deadline are rechecked before private I/O and commit. Receipt identity
    remains stable across approved resumes; copies stay private until confirmed final-step success or
    explicit success recovery. Retention never settles a step or retries business effects.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (UploadWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_execution_kind=x_gregale_operation_execution_kind,
        x_gregale_operation_workflow_run_id=x_gregale_operation_workflow_run_id,
        x_gregale_operation_workflow_step=x_gregale_operation_workflow_step,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_workflow_capability=x_gregale_operation_workflow_capability,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: UploadWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> OperationWorkflowArtifactResponse | Problem | None:
    """Upload direct private result bytes with current final workflow action authority.

     Requires a current Operations workload assertion and native final-action proof for the pinned
    workflow run, step, generation and attempt. The descriptor declares immutable report ID, filename,
    byte count and SHA-256 without a source URI or storage credential. Each copy reserves unique staging
    storage and verifies exact bytes under existing quotas and transfer budgets. Current native owner,
    cancellation, lease and deadline are rechecked before private I/O and commit. Receipt identity
    remains stable across approved resumes; copies stay private until confirmed final-step success or
    explicit success recovery. Retention never settles a step or retries business effects.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (UploadWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowArtifactResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            report_id=report_id,
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
            x_gregale_operation_execution_kind=x_gregale_operation_execution_kind,
            x_gregale_operation_workflow_run_id=x_gregale_operation_workflow_run_id,
            x_gregale_operation_workflow_step=x_gregale_operation_workflow_step,
            x_gregale_operation_generation=x_gregale_operation_generation,
            x_gregale_operation_workflow_capability=x_gregale_operation_workflow_capability,
        )
    ).parsed
