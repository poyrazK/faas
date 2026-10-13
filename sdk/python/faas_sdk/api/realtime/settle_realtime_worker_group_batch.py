from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group_batch_settlement_request import RealtimeConsumerGroupBatchSettlementRequest
from ...models.realtime_consumer_group_batch_settlement_result import RealtimeConsumerGroupBatchSettlementResult
from ...types import Response


def _get_kwargs(
    group_id: str,
    *,
    body: RealtimeConsumerGroupBatchSettlementRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/realtime/workers/groups/{group_id}/claims/settle/batch".format(
            group_id=quote(str(group_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeConsumerGroupBatchSettlementResult | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupBatchSettlementResult.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

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
) -> Response[Any | RealtimeConsumerGroupBatchSettlementResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupBatchSettlementRequest,
) -> Response[Any | RealtimeConsumerGroupBatchSettlementResult]:
    """Settle a batch of consumer group claims using scoped worker credentials

     Settle a batch of consumer group claims. Validate all 1 to 32 items before mutation; duplicate
    numeric sequences are invalid. Results preserve request order. Each valid current lease is
    completed, retried, released or renewed; expired, missing or differently owned leases return
    lease_lost without blocking other items. Scoped credentials require every action in the request,
    rechecked under the group lock; no additional grant. Account writes require ownership, deploy-write
    scope and MFA. Known lease failures commit alongside valid settlements; database errors roll back
    the batch. Optional idempotency_key saves the entire result for 24 hours; repeat the same request
    with the same key to recover a lost response. Different contents return 409
    realtime_settlement_key_conflict. Without a key, a lost response may have committed.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupBatchSettlementRequest): Realtime Consumer Group Batch
            Settlement Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupBatchSettlementResult]
    """

    kwargs = _get_kwargs(
        group_id=group_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupBatchSettlementRequest,
) -> Any | RealtimeConsumerGroupBatchSettlementResult | None:
    """Settle a batch of consumer group claims using scoped worker credentials

     Settle a batch of consumer group claims. Validate all 1 to 32 items before mutation; duplicate
    numeric sequences are invalid. Results preserve request order. Each valid current lease is
    completed, retried, released or renewed; expired, missing or differently owned leases return
    lease_lost without blocking other items. Scoped credentials require every action in the request,
    rechecked under the group lock; no additional grant. Account writes require ownership, deploy-write
    scope and MFA. Known lease failures commit alongside valid settlements; database errors roll back
    the batch. Optional idempotency_key saves the entire result for 24 hours; repeat the same request
    with the same key to recover a lost response. Different contents return 409
    realtime_settlement_key_conflict. Without a key, a lost response may have committed.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupBatchSettlementRequest): Realtime Consumer Group Batch
            Settlement Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupBatchSettlementResult
    """

    return sync_detailed(
        group_id=group_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupBatchSettlementRequest,
) -> Response[Any | RealtimeConsumerGroupBatchSettlementResult]:
    """Settle a batch of consumer group claims using scoped worker credentials

     Settle a batch of consumer group claims. Validate all 1 to 32 items before mutation; duplicate
    numeric sequences are invalid. Results preserve request order. Each valid current lease is
    completed, retried, released or renewed; expired, missing or differently owned leases return
    lease_lost without blocking other items. Scoped credentials require every action in the request,
    rechecked under the group lock; no additional grant. Account writes require ownership, deploy-write
    scope and MFA. Known lease failures commit alongside valid settlements; database errors roll back
    the batch. Optional idempotency_key saves the entire result for 24 hours; repeat the same request
    with the same key to recover a lost response. Different contents return 409
    realtime_settlement_key_conflict. Without a key, a lost response may have committed.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupBatchSettlementRequest): Realtime Consumer Group Batch
            Settlement Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupBatchSettlementResult]
    """

    kwargs = _get_kwargs(
        group_id=group_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    group_id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeConsumerGroupBatchSettlementRequest,
) -> Any | RealtimeConsumerGroupBatchSettlementResult | None:
    """Settle a batch of consumer group claims using scoped worker credentials

     Settle a batch of consumer group claims. Validate all 1 to 32 items before mutation; duplicate
    numeric sequences are invalid. Results preserve request order. Each valid current lease is
    completed, retried, released or renewed; expired, missing or differently owned leases return
    lease_lost without blocking other items. Scoped credentials require every action in the request,
    rechecked under the group lock; no additional grant. Account writes require ownership, deploy-write
    scope and MFA. Known lease failures commit alongside valid settlements; database errors roll back
    the batch. Optional idempotency_key saves the entire result for 24 hours; repeat the same request
    with the same key to recover a lost response. Different contents return 409
    realtime_settlement_key_conflict. Without a key, a lost response may have committed.

    Args:
        group_id (str):
        body (RealtimeConsumerGroupBatchSettlementRequest): Realtime Consumer Group Batch
            Settlement Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupBatchSettlementResult
    """

    return (
        await asyncio_detailed(
            group_id=group_id,
            client=client,
            body=body,
        )
    ).parsed
