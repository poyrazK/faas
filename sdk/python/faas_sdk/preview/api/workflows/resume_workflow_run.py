from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.resume_workflow_run_request import ResumeWorkflowRunRequest
from ...models.workflow_run_response import WorkflowRunResponse
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: ResumeWorkflowRunRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/workflows/runs/{id}/resume".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRunResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowRunResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[Problem | WorkflowRunResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeWorkflowRunRequest,
) -> Response[Problem | WorkflowRunResponse]:
    """Resume eligible failed actions in a durable workflow run.

     Reopens eligible failed actions with a fresh retry budget while preserving
    the original definition, inputs, successful steps, batch results, guard
    decisions, action idempotency keys and attempt history. Requires the
    current resume_count and honors Idempotency-Key for request replay.
    Cancelled runs, active calls or waits, executed failure/timeout handlers,
    failed control steps and replay-unsafe integration mutations are rejected.
    A live default deployment and valid integration bindings are required.
    Each run permits at most 16 resumptions and counts against active-run quotas.

    Args:
        id (UUID):
        body (ResumeWorkflowRunRequest): Optimistic continuation request; send the resume_count
            from the inspected run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRunResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeWorkflowRunRequest,
) -> Problem | WorkflowRunResponse | None:
    """Resume eligible failed actions in a durable workflow run.

     Reopens eligible failed actions with a fresh retry budget while preserving
    the original definition, inputs, successful steps, batch results, guard
    decisions, action idempotency keys and attempt history. Requires the
    current resume_count and honors Idempotency-Key for request replay.
    Cancelled runs, active calls or waits, executed failure/timeout handlers,
    failed control steps and replay-unsafe integration mutations are rejected.
    A live default deployment and valid integration bindings are required.
    Each run permits at most 16 resumptions and counts against active-run quotas.

    Args:
        id (UUID):
        body (ResumeWorkflowRunRequest): Optimistic continuation request; send the resume_count
            from the inspected run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRunResponse
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeWorkflowRunRequest,
) -> Response[Problem | WorkflowRunResponse]:
    """Resume eligible failed actions in a durable workflow run.

     Reopens eligible failed actions with a fresh retry budget while preserving
    the original definition, inputs, successful steps, batch results, guard
    decisions, action idempotency keys and attempt history. Requires the
    current resume_count and honors Idempotency-Key for request replay.
    Cancelled runs, active calls or waits, executed failure/timeout handlers,
    failed control steps and replay-unsafe integration mutations are rejected.
    A live default deployment and valid integration bindings are required.
    Each run permits at most 16 resumptions and counts against active-run quotas.

    Args:
        id (UUID):
        body (ResumeWorkflowRunRequest): Optimistic continuation request; send the resume_count
            from the inspected run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRunResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ResumeWorkflowRunRequest,
) -> Problem | WorkflowRunResponse | None:
    """Resume eligible failed actions in a durable workflow run.

     Reopens eligible failed actions with a fresh retry budget while preserving
    the original definition, inputs, successful steps, batch results, guard
    decisions, action idempotency keys and attempt history. Requires the
    current resume_count and honors Idempotency-Key for request replay.
    Cancelled runs, active calls or waits, executed failure/timeout handlers,
    failed control steps and replay-unsafe integration mutations are rejected.
    A live default deployment and valid integration bindings are required.
    Each run permits at most 16 resumptions and counts against active-run quotas.

    Args:
        id (UUID):
        body (ResumeWorkflowRunRequest): Optimistic continuation request; send the resume_count
            from the inspected run.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRunResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
