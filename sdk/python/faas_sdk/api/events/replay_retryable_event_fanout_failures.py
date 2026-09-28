from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.replay_retryable_event_fanout_failures_request import ReplayRetryableEventFanoutFailuresRequest
from ...models.replay_retryable_event_fanout_failures_response import ReplayRetryableEventFanoutFailuresResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ReplayRetryableEventFanoutFailuresRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-deliveries:replay-retryable-fanout-failures".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ReplayRetryableEventFanoutFailuresResponse | None:
    if response.status_code == 202:
        response_202 = ReplayRetryableEventFanoutFailuresResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ReplayRetryableEventFanoutFailuresResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayRetryableEventFanoutFailuresRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | ReplayRetryableEventFanoutFailuresResponse]:
    """Replay a bounded batch of retryable event recipient failures.

     Requeues up to 100 terminal recipients for the app whose stable failure
    classification marks them retryable. Optional event_source and event_id
    narrow the action to one published event and must be supplied together.
    Only settled event receipts are selected; non-retryable and
    still-processing recipients are left alone. Repeat the request while
    has_more is true to drain a larger batch.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayRetryableEventFanoutFailuresRequest): Bounded app-scoped replay of failures
            classified as retryable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReplayRetryableEventFanoutFailuresResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayRetryableEventFanoutFailuresRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | ReplayRetryableEventFanoutFailuresResponse | None:
    """Replay a bounded batch of retryable event recipient failures.

     Requeues up to 100 terminal recipients for the app whose stable failure
    classification marks them retryable. Optional event_source and event_id
    narrow the action to one published event and must be supplied together.
    Only settled event receipts are selected; non-retryable and
    still-processing recipients are left alone. Repeat the request while
    has_more is true to drain a larger batch.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayRetryableEventFanoutFailuresRequest): Bounded app-scoped replay of failures
            classified as retryable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReplayRetryableEventFanoutFailuresResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayRetryableEventFanoutFailuresRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | ReplayRetryableEventFanoutFailuresResponse]:
    """Replay a bounded batch of retryable event recipient failures.

     Requeues up to 100 terminal recipients for the app whose stable failure
    classification marks them retryable. Optional event_source and event_id
    narrow the action to one published event and must be supplied together.
    Only settled event receipts are selected; non-retryable and
    still-processing recipients are left alone. Repeat the request while
    has_more is true to drain a larger batch.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayRetryableEventFanoutFailuresRequest): Bounded app-scoped replay of failures
            classified as retryable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReplayRetryableEventFanoutFailuresResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayRetryableEventFanoutFailuresRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | ReplayRetryableEventFanoutFailuresResponse | None:
    """Replay a bounded batch of retryable event recipient failures.

     Requeues up to 100 terminal recipients for the app whose stable failure
    classification marks them retryable. Optional event_source and event_id
    narrow the action to one published event and must be supplied together.
    Only settled event receipts are selected; non-retryable and
    still-processing recipients are left alone. Repeat the request while
    has_more is true to drain a larger batch.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayRetryableEventFanoutFailuresRequest): Bounded app-scoped replay of failures
            classified as retryable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReplayRetryableEventFanoutFailuresResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
