from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_replay_backfill_retry_request import EventReplayBackfillRetryRequest
from ...models.event_replay_backfill_retry_response import EventReplayBackfillRetryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    job_id: UUID,
    *,
    body: EventReplayBackfillRetryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/event-replays/{job_id}/retry-failed".format(
            job_id=quote(str(job_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReplayBackfillRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventReplayBackfillRetryResponse.from_dict(response.json())

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
) -> Response[EventReplayBackfillRetryResponse | Problem]:
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
    body: EventReplayBackfillRetryRequest,
) -> Response[EventReplayBackfillRetryResponse | Problem]:
    """Retry a bounded batch of failed backfill deliveries.

     Resets up to 100 failed routing recipients in this job with a fresh routing generation; handler
    retries and dead letters remain independent.

    Args:
        job_id (UUID):
        body (EventReplayBackfillRetryRequest): Bound for the number of failed routing recipients
            requeued in this call.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayBackfillRetryResponse | Problem]
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
    body: EventReplayBackfillRetryRequest,
) -> EventReplayBackfillRetryResponse | Problem | None:
    """Retry a bounded batch of failed backfill deliveries.

     Resets up to 100 failed routing recipients in this job with a fresh routing generation; handler
    retries and dead letters remain independent.

    Args:
        job_id (UUID):
        body (EventReplayBackfillRetryRequest): Bound for the number of failed routing recipients
            requeued in this call.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayBackfillRetryResponse | Problem
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
    body: EventReplayBackfillRetryRequest,
) -> Response[EventReplayBackfillRetryResponse | Problem]:
    """Retry a bounded batch of failed backfill deliveries.

     Resets up to 100 failed routing recipients in this job with a fresh routing generation; handler
    retries and dead letters remain independent.

    Args:
        job_id (UUID):
        body (EventReplayBackfillRetryRequest): Bound for the number of failed routing recipients
            requeued in this call.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayBackfillRetryResponse | Problem]
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
    body: EventReplayBackfillRetryRequest,
) -> EventReplayBackfillRetryResponse | Problem | None:
    """Retry a bounded batch of failed backfill deliveries.

     Resets up to 100 failed routing recipients in this job with a fresh routing generation; handler
    retries and dead letters remain independent.

    Args:
        job_id (UUID):
        body (EventReplayBackfillRetryRequest): Bound for the number of failed routing recipients
            requeued in this call.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayBackfillRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            body=body,
        )
    ).parsed
