from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_dead_letter_bulk_replay_request import RealtimeDeadLetterBulkReplayRequest
from ...models.realtime_dead_letter_bulk_replay_result import RealtimeDeadLetterBulkReplayResult
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeDeadLetterBulkReplayRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/replay".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | RealtimeDeadLetterBulkReplayResult | None:
    if response.status_code == 200:
        response_200 = RealtimeDeadLetterBulkReplayResult.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | Problem | RealtimeDeadLetterBulkReplayResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeDeadLetterBulkReplayRequest,
) -> Response[Any | Problem | RealtimeDeadLetterBulkReplayResult]:
    """Replay selected dead letters

     Deploy-write scope, app ownership and MFA required. Select 1 to 32 unique sequences with exact
    dead_since timestamps from preview and the immutable group UUID. Preserves ordering checks and
    resets attempts for successful replays. Outcomes are replayed, ordering_blocked, stale, not_dead,
    missing, deadline_expired or invalid_deadline. Missing may mean retention cleanup; no cause is
    inferred. Recognized per-item conflicts do not prevent other selected replays. Unexpected failures
    roll back the whole transaction. Group UUID mismatch returns 409. A replayed item gets a fresh
    fencing token on its next claim. Do not automatically retry an uncertain response.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeDeadLetterBulkReplayRequest): Realtime Dead Letter Bulk Replay Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeDeadLetterBulkReplayResult]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeDeadLetterBulkReplayRequest,
) -> Any | Problem | RealtimeDeadLetterBulkReplayResult | None:
    """Replay selected dead letters

     Deploy-write scope, app ownership and MFA required. Select 1 to 32 unique sequences with exact
    dead_since timestamps from preview and the immutable group UUID. Preserves ordering checks and
    resets attempts for successful replays. Outcomes are replayed, ordering_blocked, stale, not_dead,
    missing, deadline_expired or invalid_deadline. Missing may mean retention cleanup; no cause is
    inferred. Recognized per-item conflicts do not prevent other selected replays. Unexpected failures
    roll back the whole transaction. Group UUID mismatch returns 409. A replayed item gets a fresh
    fencing token on its next claim. Do not automatically retry an uncertain response.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeDeadLetterBulkReplayRequest): Realtime Dead Letter Bulk Replay Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeDeadLetterBulkReplayResult
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeDeadLetterBulkReplayRequest,
) -> Response[Any | Problem | RealtimeDeadLetterBulkReplayResult]:
    """Replay selected dead letters

     Deploy-write scope, app ownership and MFA required. Select 1 to 32 unique sequences with exact
    dead_since timestamps from preview and the immutable group UUID. Preserves ordering checks and
    resets attempts for successful replays. Outcomes are replayed, ordering_blocked, stale, not_dead,
    missing, deadline_expired or invalid_deadline. Missing may mean retention cleanup; no cause is
    inferred. Recognized per-item conflicts do not prevent other selected replays. Unexpected failures
    roll back the whole transaction. Group UUID mismatch returns 409. A replayed item gets a fresh
    fencing token on its next claim. Do not automatically retry an uncertain response.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeDeadLetterBulkReplayRequest): Realtime Dead Letter Bulk Replay Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeDeadLetterBulkReplayResult]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeDeadLetterBulkReplayRequest,
) -> Any | Problem | RealtimeDeadLetterBulkReplayResult | None:
    """Replay selected dead letters

     Deploy-write scope, app ownership and MFA required. Select 1 to 32 unique sequences with exact
    dead_since timestamps from preview and the immutable group UUID. Preserves ordering checks and
    resets attempts for successful replays. Outcomes are replayed, ordering_blocked, stale, not_dead,
    missing, deadline_expired or invalid_deadline. Missing may mean retention cleanup; no cause is
    inferred. Recognized per-item conflicts do not prevent other selected replays. Unexpected failures
    roll back the whole transaction. Group UUID mismatch returns 409. A replayed item gets a fresh
    fencing token on its next claim. Do not automatically retry an uncertain response.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeDeadLetterBulkReplayRequest): Realtime Dead Letter Bulk Replay Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeDeadLetterBulkReplayResult
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            client=client,
            body=body,
        )
    ).parsed
