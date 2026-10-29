from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_recovery_cancellation_request import WorkflowRecoveryCancellationRequest
from ...models.workflow_recovery_cancellation_response import WorkflowRecoveryCancellationResponse
from ...types import Response


def _get_kwargs(
    job_id: UUID,
    *,
    body: WorkflowRecoveryCancellationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/event-recoveries/{job_id}/workflow-cancel".format(
            job_id=quote(str(job_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryCancellationResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryCancellationResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryCancellationResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowRecoveryCancellationRequest,
) -> Response[Problem | WorkflowRecoveryCancellationResponse]:
    """Cancel one reviewed admitted workflow generation

     Cancellation commit for native workflow recovery only. Rechecks the frozen item and generation under
    the run lock. Changed or unproven generations are refused. Cancellation is cooperative and cannot
    undo external side effects. Successful cancellation and its actor and reason are recorded atomically
    in recovery history.

    Args:
        job_id (UUID):
        body (WorkflowRecoveryCancellationRequest): Exact admitted recovery item and generation
            reviewed by an operator. Preview is advisory and commit rechecks eligibility. Operator
            identity is resolved from authentication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryCancellationResponse]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowRecoveryCancellationRequest,
) -> Problem | WorkflowRecoveryCancellationResponse | None:
    """Cancel one reviewed admitted workflow generation

     Cancellation commit for native workflow recovery only. Rechecks the frozen item and generation under
    the run lock. Changed or unproven generations are refused. Cancellation is cooperative and cannot
    undo external side effects. Successful cancellation and its actor and reason are recorded atomically
    in recovery history.

    Args:
        job_id (UUID):
        body (WorkflowRecoveryCancellationRequest): Exact admitted recovery item and generation
            reviewed by an operator. Preview is advisory and commit rechecks eligibility. Operator
            identity is resolved from authentication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryCancellationResponse
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowRecoveryCancellationRequest,
) -> Response[Problem | WorkflowRecoveryCancellationResponse]:
    """Cancel one reviewed admitted workflow generation

     Cancellation commit for native workflow recovery only. Rechecks the frozen item and generation under
    the run lock. Changed or unproven generations are refused. Cancellation is cooperative and cannot
    undo external side effects. Successful cancellation and its actor and reason are recorded atomically
    in recovery history.

    Args:
        job_id (UUID):
        body (WorkflowRecoveryCancellationRequest): Exact admitted recovery item and generation
            reviewed by an operator. Preview is advisory and commit rechecks eligibility. Operator
            identity is resolved from authentication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryCancellationResponse]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowRecoveryCancellationRequest,
) -> Problem | WorkflowRecoveryCancellationResponse | None:
    """Cancel one reviewed admitted workflow generation

     Cancellation commit for native workflow recovery only. Rechecks the frozen item and generation under
    the run lock. Changed or unproven generations are refused. Cancellation is cooperative and cannot
    undo external side effects. Successful cancellation and its actor and reason are recorded atomically
    in recovery history.

    Args:
        job_id (UUID):
        body (WorkflowRecoveryCancellationRequest): Exact admitted recovery item and generation
            reviewed by an operator. Preview is advisory and commit rechecks eligibility. Operator
            identity is resolved from authentication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryCancellationResponse
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            body=body,
        )
    ).parsed
