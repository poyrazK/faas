from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_state_validation_request import OperationWorkflowStateValidationRequest
from ...models.operation_workflow_state_validation_response import OperationWorkflowStateValidationResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationWorkflowStateValidationRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Faas-Invocation-Id"] = str(x_faas_invocation_id)

    headers["X-Gregale-Operation-Attempt"] = str(x_gregale_operation_attempt)

    headers["X-Gregale-Operation-Capability"] = x_gregale_operation_capability

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/operations/{id}/workflow-states/validate".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowStateValidationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowStateValidationResponse.from_dict(response.json())

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
) -> Response[OperationWorkflowStateValidationResponse | Problem]:
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
    body: OperationWorkflowStateValidationRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> Response[OperationWorkflowStateValidationResponse | Problem]:
    """Validate app-reported workflow states before transaction commit.

     Requires the active workload and invocation claim. Validates the workflow contract version,
    transition edge, required milestone evidence and transaction-assigned revisions before commit.
    Evidence IDs must refer to facts in the same submitted transaction batch. This does not publish
    facts or reserve capacity. Maximum request size is 131072 bytes.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationWorkflowStateValidationRequest): Batch of application-reported workflow
            updates to validate before their transaction commits.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowStateValidationResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowStateValidationRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> OperationWorkflowStateValidationResponse | Problem | None:
    """Validate app-reported workflow states before transaction commit.

     Requires the active workload and invocation claim. Validates the workflow contract version,
    transition edge, required milestone evidence and transaction-assigned revisions before commit.
    Evidence IDs must refer to facts in the same submitted transaction batch. This does not publish
    facts or reserve capacity. Maximum request size is 131072 bytes.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationWorkflowStateValidationRequest): Batch of application-reported workflow
            updates to validate before their transaction commits.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowStateValidationResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowStateValidationRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> Response[OperationWorkflowStateValidationResponse | Problem]:
    """Validate app-reported workflow states before transaction commit.

     Requires the active workload and invocation claim. Validates the workflow contract version,
    transition edge, required milestone evidence and transaction-assigned revisions before commit.
    Evidence IDs must refer to facts in the same submitted transaction batch. This does not publish
    facts or reserve capacity. Maximum request size is 131072 bytes.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationWorkflowStateValidationRequest): Batch of application-reported workflow
            updates to validate before their transaction commits.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowStateValidationResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowStateValidationRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> OperationWorkflowStateValidationResponse | Problem | None:
    """Validate app-reported workflow states before transaction commit.

     Requires the active workload and invocation claim. Validates the workflow contract version,
    transition edge, required milestone evidence and transaction-assigned revisions before commit.
    Evidence IDs must refer to facts in the same submitted transaction batch. This does not publish
    facts or reserve capacity. Maximum request size is 131072 bytes.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationWorkflowStateValidationRequest): Batch of application-reported workflow
            updates to validate before their transaction commits.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowStateValidationResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            x_faas_invocation_id=x_faas_invocation_id,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
            x_gregale_operation_capability=x_gregale_operation_capability,
        )
    ).parsed
