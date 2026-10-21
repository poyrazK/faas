from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cancel_event_execution_lineage_retry_job_request import CancelEventExecutionLineageRetryJobRequest
from ...models.event_execution_lineage_retry_job import EventExecutionLineageRetryJob
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    body: CancelEventExecutionLineageRetryJobRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-reconciliation/retry-jobs/{request_id}/cancel".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventExecutionLineageRetryJob | Problem | None:
    if response.status_code == 200:
        response_200 = EventExecutionLineageRetryJob.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventExecutionLineageRetryJob | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CancelEventExecutionLineageRetryJobRequest,
) -> Response[EventExecutionLineageRetryJob | Problem]:
    """Cancel unprocessed cases in a retained bulk lineage retry job.

     Requires deploy/write scope and MFA. The original creation timestamp fences the job incarnation.
    Cancellation accepts pending or paused jobs and waits for the scheduler job lock, preserves
    committed evaluations and marks remaining items cancelled. Processed counts evaluated items only.
    Repeating cancellation with the same original timestamp returns the unchanged terminal job; a
    completed job stays completed. No evaluation can start for this job after cancellation commits.
    Existing repairs remain applied. Cancellation does not extend retention, change case revisions or
    reset coverage. Reusing the original submission UUID and selection returns the cancelled job without
    restarting it. Optional nonzero command_id atomically records an immutable cancellation
    acknowledgment with the state change. Repeating the same UUID/action/creation guard returns current
    job state with the original control_receipt. A completed or already cancelled job remains unchanged
    and a new UUID records that terminal acknowledgment. Reusing a pause/resume command UUID returns 409
    event_lineage_retry_control_command_changed. The shared cap is 128 receipts per job; a new UUID
    beyond the cap returns 429 without cancelling, while known command repeats and cancellation without
    command_id remain available. Receipts expire with the original job.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (CancelEventExecutionLineageRetryJobRequest): Original job creation guard and
            optional command identity for cancelling bulk lineage retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageRetryJob | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CancelEventExecutionLineageRetryJobRequest,
) -> EventExecutionLineageRetryJob | Problem | None:
    """Cancel unprocessed cases in a retained bulk lineage retry job.

     Requires deploy/write scope and MFA. The original creation timestamp fences the job incarnation.
    Cancellation accepts pending or paused jobs and waits for the scheduler job lock, preserves
    committed evaluations and marks remaining items cancelled. Processed counts evaluated items only.
    Repeating cancellation with the same original timestamp returns the unchanged terminal job; a
    completed job stays completed. No evaluation can start for this job after cancellation commits.
    Existing repairs remain applied. Cancellation does not extend retention, change case revisions or
    reset coverage. Reusing the original submission UUID and selection returns the cancelled job without
    restarting it. Optional nonzero command_id atomically records an immutable cancellation
    acknowledgment with the state change. Repeating the same UUID/action/creation guard returns current
    job state with the original control_receipt. A completed or already cancelled job remains unchanged
    and a new UUID records that terminal acknowledgment. Reusing a pause/resume command UUID returns 409
    event_lineage_retry_control_command_changed. The shared cap is 128 receipts per job; a new UUID
    beyond the cap returns 429 without cancelling, while known command repeats and cancellation without
    command_id remain available. Receipts expire with the original job.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (CancelEventExecutionLineageRetryJobRequest): Original job creation guard and
            optional command identity for cancelling bulk lineage retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageRetryJob | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CancelEventExecutionLineageRetryJobRequest,
) -> Response[EventExecutionLineageRetryJob | Problem]:
    """Cancel unprocessed cases in a retained bulk lineage retry job.

     Requires deploy/write scope and MFA. The original creation timestamp fences the job incarnation.
    Cancellation accepts pending or paused jobs and waits for the scheduler job lock, preserves
    committed evaluations and marks remaining items cancelled. Processed counts evaluated items only.
    Repeating cancellation with the same original timestamp returns the unchanged terminal job; a
    completed job stays completed. No evaluation can start for this job after cancellation commits.
    Existing repairs remain applied. Cancellation does not extend retention, change case revisions or
    reset coverage. Reusing the original submission UUID and selection returns the cancelled job without
    restarting it. Optional nonzero command_id atomically records an immutable cancellation
    acknowledgment with the state change. Repeating the same UUID/action/creation guard returns current
    job state with the original control_receipt. A completed or already cancelled job remains unchanged
    and a new UUID records that terminal acknowledgment. Reusing a pause/resume command UUID returns 409
    event_lineage_retry_control_command_changed. The shared cap is 128 receipts per job; a new UUID
    beyond the cap returns 429 without cancelling, while known command repeats and cancellation without
    command_id remain available. Receipts expire with the original job.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (CancelEventExecutionLineageRetryJobRequest): Original job creation guard and
            optional command identity for cancelling bulk lineage retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageRetryJob | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CancelEventExecutionLineageRetryJobRequest,
) -> EventExecutionLineageRetryJob | Problem | None:
    """Cancel unprocessed cases in a retained bulk lineage retry job.

     Requires deploy/write scope and MFA. The original creation timestamp fences the job incarnation.
    Cancellation accepts pending or paused jobs and waits for the scheduler job lock, preserves
    committed evaluations and marks remaining items cancelled. Processed counts evaluated items only.
    Repeating cancellation with the same original timestamp returns the unchanged terminal job; a
    completed job stays completed. No evaluation can start for this job after cancellation commits.
    Existing repairs remain applied. Cancellation does not extend retention, change case revisions or
    reset coverage. Reusing the original submission UUID and selection returns the cancelled job without
    restarting it. Optional nonzero command_id atomically records an immutable cancellation
    acknowledgment with the state change. Repeating the same UUID/action/creation guard returns current
    job state with the original control_receipt. A completed or already cancelled job remains unchanged
    and a new UUID records that terminal acknowledgment. Reusing a pause/resume command UUID returns 409
    event_lineage_retry_control_command_changed. The shared cap is 128 receipts per job; a new UUID
    beyond the cap returns 429 without cancelling, while known command repeats and cancellation without
    command_id remain available. Receipts expire with the original job.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (CancelEventExecutionLineageRetryJobRequest): Original job creation guard and
            optional command identity for cancelling bulk lineage retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageRetryJob | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            request_id=request_id,
            client=client,
            body=body,
        )
    ).parsed
