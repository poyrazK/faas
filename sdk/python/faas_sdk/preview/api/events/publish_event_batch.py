from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.publish_event_batch_request import PublishEventBatchRequest
from ...models.publish_event_batch_response import PublishEventBatchResponse
from ...types import Response


def _get_kwargs(
    *,
    body: PublishEventBatchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/events:publish-batch",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | PublishEventBatchResponse | None:
    if response.status_code == 200:
        response_200 = PublishEventBatchResponse.from_dict(response.json())

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
) -> Response[Problem | PublishEventBatchResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: PublishEventBatchRequest,
) -> Response[Problem | PublishEventBatchResponse]:
    """Publish up to 100 independent events in input order.

     Accepts a nonempty batch of at most 100 events in a body of at most
    1 MiB. Authentication, MFA and events:publish/deploy:write/admin scopes
    match single-event publication. Invalid outer JSON, size or count
    rejects the entire request before acceptance. Each item otherwise has
    its own transaction, schema validation, identity and storage charge.
    Results use zero-based input indexes in input order. accepted and
    duplicate include a receipt with the original acceptance timestamp.
    rejected includes a problem; unknown means acceptance could not be
    confirmed, including an interrupted commit. Retry retryable items or
    an unanswered request with exactly the original source/id/content.
    Stable per-event identities provide deduplication; there is no batch
    transaction or request-wide Idempotency-Key replay. Processing is
    sequential with a 30-second budget; unattempted items are rejected
    with retryable=true and code event_publish_not_attempted.
    Newly accepted items preserve their input acceptance order. Duplicates
    retain their original position; rejections create none. Concurrent
    requests may interleave. Execution ordering remains opt-in per keyed
    lane, and delivery remains at least once.

    Args:
        body (PublishEventBatchRequest): Bounded collection of independently accepted event
            envelopes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublishEventBatchResponse]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: PublishEventBatchRequest,
) -> Problem | PublishEventBatchResponse | None:
    """Publish up to 100 independent events in input order.

     Accepts a nonempty batch of at most 100 events in a body of at most
    1 MiB. Authentication, MFA and events:publish/deploy:write/admin scopes
    match single-event publication. Invalid outer JSON, size or count
    rejects the entire request before acceptance. Each item otherwise has
    its own transaction, schema validation, identity and storage charge.
    Results use zero-based input indexes in input order. accepted and
    duplicate include a receipt with the original acceptance timestamp.
    rejected includes a problem; unknown means acceptance could not be
    confirmed, including an interrupted commit. Retry retryable items or
    an unanswered request with exactly the original source/id/content.
    Stable per-event identities provide deduplication; there is no batch
    transaction or request-wide Idempotency-Key replay. Processing is
    sequential with a 30-second budget; unattempted items are rejected
    with retryable=true and code event_publish_not_attempted.
    Newly accepted items preserve their input acceptance order. Duplicates
    retain their original position; rejections create none. Concurrent
    requests may interleave. Execution ordering remains opt-in per keyed
    lane, and delivery remains at least once.

    Args:
        body (PublishEventBatchRequest): Bounded collection of independently accepted event
            envelopes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublishEventBatchResponse
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: PublishEventBatchRequest,
) -> Response[Problem | PublishEventBatchResponse]:
    """Publish up to 100 independent events in input order.

     Accepts a nonempty batch of at most 100 events in a body of at most
    1 MiB. Authentication, MFA and events:publish/deploy:write/admin scopes
    match single-event publication. Invalid outer JSON, size or count
    rejects the entire request before acceptance. Each item otherwise has
    its own transaction, schema validation, identity and storage charge.
    Results use zero-based input indexes in input order. accepted and
    duplicate include a receipt with the original acceptance timestamp.
    rejected includes a problem; unknown means acceptance could not be
    confirmed, including an interrupted commit. Retry retryable items or
    an unanswered request with exactly the original source/id/content.
    Stable per-event identities provide deduplication; there is no batch
    transaction or request-wide Idempotency-Key replay. Processing is
    sequential with a 30-second budget; unattempted items are rejected
    with retryable=true and code event_publish_not_attempted.
    Newly accepted items preserve their input acceptance order. Duplicates
    retain their original position; rejections create none. Concurrent
    requests may interleave. Execution ordering remains opt-in per keyed
    lane, and delivery remains at least once.

    Args:
        body (PublishEventBatchRequest): Bounded collection of independently accepted event
            envelopes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublishEventBatchResponse]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: PublishEventBatchRequest,
) -> Problem | PublishEventBatchResponse | None:
    """Publish up to 100 independent events in input order.

     Accepts a nonempty batch of at most 100 events in a body of at most
    1 MiB. Authentication, MFA and events:publish/deploy:write/admin scopes
    match single-event publication. Invalid outer JSON, size or count
    rejects the entire request before acceptance. Each item otherwise has
    its own transaction, schema validation, identity and storage charge.
    Results use zero-based input indexes in input order. accepted and
    duplicate include a receipt with the original acceptance timestamp.
    rejected includes a problem; unknown means acceptance could not be
    confirmed, including an interrupted commit. Retry retryable items or
    an unanswered request with exactly the original source/id/content.
    Stable per-event identities provide deduplication; there is no batch
    transaction or request-wide Idempotency-Key replay. Processing is
    sequential with a 30-second budget; unattempted items are rejected
    with retryable=true and code event_publish_not_attempted.
    Newly accepted items preserve their input acceptance order. Duplicates
    retain their original position; rejections create none. Concurrent
    requests may interleave. Execution ordering remains opt-in per keyed
    lane, and delivery remains at least once.

    Args:
        body (PublishEventBatchRequest): Bounded collection of independently accepted event
            envelopes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublishEventBatchResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
