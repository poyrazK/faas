from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_batch import RealtimeConsumerGroupBatch
from ...models.realtime_consumer_group_batch_request import RealtimeConsumerGroupBatchRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeConsumerGroupBatchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/batch".format(
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
) -> Any | Problem | RealtimeConsumerGroupBatch | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupBatch.from_dict(response.json())

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
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | Problem | RealtimeConsumerGroupBatch]:
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
    body: RealtimeConsumerGroupBatchRequest,
) -> Response[Any | Problem | RealtimeConsumerGroupBatch]:
    """Claim a bounded batch with independent leases

     Claim 1–16 messages in one transaction, bounded by the group-wide 32 active leases and 128
    outstanding snapshots. Each message has its own token and settlement. Always returns 200 with a
    possibly empty claims array. Partial batches may report history_unavailable or capacity_blocked;
    process returned claims before resolving the blocker. Empty claims can mean caught up, delayed
    retries or full lease slots. Requires the existing claim permission. Tokens are returned once; no
    response caching or automatic claim retries.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupBatchRequest): Realtime Consumer Group Batch Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupBatch]
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
    body: RealtimeConsumerGroupBatchRequest,
) -> Any | Problem | RealtimeConsumerGroupBatch | None:
    """Claim a bounded batch with independent leases

     Claim 1–16 messages in one transaction, bounded by the group-wide 32 active leases and 128
    outstanding snapshots. Each message has its own token and settlement. Always returns 200 with a
    possibly empty claims array. Partial batches may report history_unavailable or capacity_blocked;
    process returned claims before resolving the blocker. Empty claims can mean caught up, delayed
    retries or full lease slots. Requires the existing claim permission. Tokens are returned once; no
    response caching or automatic claim retries.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupBatchRequest): Realtime Consumer Group Batch Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupBatch
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
    body: RealtimeConsumerGroupBatchRequest,
) -> Response[Any | Problem | RealtimeConsumerGroupBatch]:
    """Claim a bounded batch with independent leases

     Claim 1–16 messages in one transaction, bounded by the group-wide 32 active leases and 128
    outstanding snapshots. Each message has its own token and settlement. Always returns 200 with a
    possibly empty claims array. Partial batches may report history_unavailable or capacity_blocked;
    process returned claims before resolving the blocker. Empty claims can mean caught up, delayed
    retries or full lease slots. Requires the existing claim permission. Tokens are returned once; no
    response caching or automatic claim retries.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupBatchRequest): Realtime Consumer Group Batch Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupBatch]
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
    body: RealtimeConsumerGroupBatchRequest,
) -> Any | Problem | RealtimeConsumerGroupBatch | None:
    """Claim a bounded batch with independent leases

     Claim 1–16 messages in one transaction, bounded by the group-wide 32 active leases and 128
    outstanding snapshots. Each message has its own token and settlement. Always returns 200 with a
    possibly empty claims array. Partial batches may report history_unavailable or capacity_blocked;
    process returned claims before resolving the blocker. Empty claims can mean caught up, delayed
    retries or full lease slots. Requires the existing claim permission. Tokens are returned once; no
    response caching or automatic claim retries.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupBatchRequest): Realtime Consumer Group Batch Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupBatch
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
