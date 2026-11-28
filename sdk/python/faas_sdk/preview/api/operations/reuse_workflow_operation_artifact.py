from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_artifact_request import OperationArtifactRequest
from ...models.operation_workflow_artifact_response import OperationWorkflowArtifactResponse
from ...models.problem import Problem
from ...models.reuse_workflow_operation_artifact_x_gregale_operation_execution_kind import (
    ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind,
)
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationArtifactRequest,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind,
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

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/workflow-operations/{id}/artifact-receipts".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
    body: OperationArtifactRequest,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> Response[OperationWorkflowArtifactResponse | Problem]:
    """Check or reuse a verified workflow result copy.

     Requires workload identity and a current final-step proof. An explicit available=false authorizes a
    new upload. A matching durable receipt is rebound to the current attempt without rereading or
    rewriting the source. Invalid or expired proofs fail; they never return available=false.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
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
    body: OperationArtifactRequest,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> OperationWorkflowArtifactResponse | Problem | None:
    """Check or reuse a verified workflow result copy.

     Requires workload identity and a current final-step proof. An explicit available=false authorizes a
    new upload. A matching durable receipt is rebound to the current attempt without rereading or
    rewriting the source. Invalid or expired proofs fail; they never return available=false.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

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
    body: OperationArtifactRequest,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> Response[OperationWorkflowArtifactResponse | Problem]:
    """Check or reuse a verified workflow result copy.

     Requires workload identity and a current final-step proof. An explicit available=false authorizes a
    new upload. A matching durable receipt is rebound to the current attempt without rereading or
    rewriting the source. Invalid or expired proofs fail; they never return available=false.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
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
    body: OperationArtifactRequest,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> OperationWorkflowArtifactResponse | Problem | None:
    """Check or reuse a verified workflow result copy.

     Requires workload identity and a current final-step proof. An explicit available=false authorizes a
    new upload. A matching durable receipt is rebound to the current attempt without rereading or
    rewriting the source. Invalid or expired proofs fail; they never return available=false.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (ReuseWorkflowOperationArtifactXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

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
            x_gregale_operation_attempt=x_gregale_operation_attempt,
            x_gregale_operation_execution_kind=x_gregale_operation_execution_kind,
            x_gregale_operation_workflow_run_id=x_gregale_operation_workflow_run_id,
            x_gregale_operation_workflow_step=x_gregale_operation_workflow_step,
            x_gregale_operation_generation=x_gregale_operation_generation,
            x_gregale_operation_workflow_capability=x_gregale_operation_workflow_capability,
        )
    ).parsed
