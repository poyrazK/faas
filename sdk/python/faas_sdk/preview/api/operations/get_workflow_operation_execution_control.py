from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_workflow_operation_execution_control_x_gregale_operation_execution_kind import (
    GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind,
)
from ...models.operation_workflow_control_response import OperationWorkflowControlResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind,
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
        "method": "get",
        "url": "/v1/runtime/workflow-operations/{id}/control".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowControlResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowControlResponse.from_dict(response.json())

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
) -> Response[OperationWorkflowControlResponse | Problem]:
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
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> Response[OperationWorkflowControlResponse | Problem]:
    """Observe cancellation and time bounds for a native workflow attempt.

     Requires a fresh Operations workload assertion and current native run, step, recovery generation,
    attempt and capability proof on the pinned deployment. Available to every linear HTTP action of a
    workflow Operation. The fixed deadline is current attempt started_at plus the captured step timeout
    (30 seconds when omitted). The usable budget ends at the earlier native lease or deadline. Reads
    never renew leases, consume reports or authorize retries. Cancellation atomically interrupts native
    authority; a denied control read also stops the cooperating handler. Existing verified copies remain
    retained under normal recovery policy. Available while new admission is closed.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowControlResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
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
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> OperationWorkflowControlResponse | Problem | None:
    """Observe cancellation and time bounds for a native workflow attempt.

     Requires a fresh Operations workload assertion and current native run, step, recovery generation,
    attempt and capability proof on the pinned deployment. Available to every linear HTTP action of a
    workflow Operation. The fixed deadline is current attempt started_at plus the captured step timeout
    (30 seconds when omitted). The usable budget ends at the earlier native lease or deadline. Reads
    never renew leases, consume reports or authorize retries. Cancellation atomically interrupts native
    authority; a denied control read also stops the cooperating handler. Existing verified copies remain
    retained under normal recovery policy. Available while new admission is closed.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowControlResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
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
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> Response[OperationWorkflowControlResponse | Problem]:
    """Observe cancellation and time bounds for a native workflow attempt.

     Requires a fresh Operations workload assertion and current native run, step, recovery generation,
    attempt and capability proof on the pinned deployment. Available to every linear HTTP action of a
    workflow Operation. The fixed deadline is current attempt started_at plus the captured step timeout
    (30 seconds when omitted). The usable budget ends at the earlier native lease or deadline. Reads
    never renew leases, consume reports or authorize retries. Cancellation atomically interrupts native
    authority; a denied control read also stops the cooperating handler. Existing verified copies remain
    retained under normal recovery policy. Available while new admission is closed.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowControlResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
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
    x_gregale_operation_attempt: int,
    x_gregale_operation_execution_kind: GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind,
    x_gregale_operation_workflow_run_id: str,
    x_gregale_operation_workflow_step: str,
    x_gregale_operation_generation: int,
    x_gregale_operation_workflow_capability: str,
) -> OperationWorkflowControlResponse | Problem | None:
    """Observe cancellation and time bounds for a native workflow attempt.

     Requires a fresh Operations workload assertion and current native run, step, recovery generation,
    attempt and capability proof on the pinned deployment. Available to every linear HTTP action of a
    workflow Operation. The fixed deadline is current attempt started_at plus the captured step timeout
    (30 seconds when omitted). The usable budget ends at the earlier native lease or deadline. Reads
    never renew leases, consume reports or authorize retries. Cancellation atomically interrupts native
    authority; a denied control read also stops the cooperating handler. Existing verified copies remain
    retained under normal recovery policy. Available while new admission is closed.

    Args:
        id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_execution_kind
            (GetWorkflowOperationExecutionControlXGregaleOperationExecutionKind):
        x_gregale_operation_workflow_run_id (str):
        x_gregale_operation_workflow_step (str):
        x_gregale_operation_generation (int):
        x_gregale_operation_workflow_capability (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowControlResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
            x_gregale_operation_execution_kind=x_gregale_operation_execution_kind,
            x_gregale_operation_workflow_run_id=x_gregale_operation_workflow_run_id,
            x_gregale_operation_workflow_step=x_gregale_operation_workflow_step,
            x_gregale_operation_generation=x_gregale_operation_generation,
            x_gregale_operation_workflow_capability=x_gregale_operation_workflow_capability,
        )
    ).parsed
