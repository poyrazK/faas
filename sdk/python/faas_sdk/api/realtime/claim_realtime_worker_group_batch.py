from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_batch import RealtimeConsumerGroupBatch
from ...models.realtime_worker_batch_claim_request import RealtimeWorkerBatchClaimRequest
from ...types import Response


def _get_kwargs(
    group_id: str,
    *,
    body: RealtimeWorkerBatchClaimRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/realtime/workers/groups/{group_id}/claims/batch".format(
            group_id=quote(str(group_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RealtimeConsumerGroupBatch | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupBatch.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RealtimeConsumerGroupBatch]:
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
    body: RealtimeWorkerBatchClaimRequest,
) -> Response[Problem | RealtimeConsumerGroupBatch]:
    """Claim a bounded batch with independent leases using scoped worker credentials

     Claim a bounded batch with independent leases. Claim 1–16 messages in one transaction, bounded by
    the group-wide 32 active leases and 128 outstanding snapshots. Each message has its own token and
    settlement. Always returns 200 with a possibly empty claims array. Partial batches may report
    history_unavailable or capacity_blocked; process returned claims before resolving the blocker. Empty
    claims can mean caught up, delayed retries or full lease slots. Requires the existing claim
    permission. Tokens are returned once; no response caching or automatic claim retries.

    Args:
        group_id (str):
        body (RealtimeWorkerBatchClaimRequest): Realtime Worker Batch Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeConsumerGroupBatch]
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
    body: RealtimeWorkerBatchClaimRequest,
) -> Problem | RealtimeConsumerGroupBatch | None:
    """Claim a bounded batch with independent leases using scoped worker credentials

     Claim a bounded batch with independent leases. Claim 1–16 messages in one transaction, bounded by
    the group-wide 32 active leases and 128 outstanding snapshots. Each message has its own token and
    settlement. Always returns 200 with a possibly empty claims array. Partial batches may report
    history_unavailable or capacity_blocked; process returned claims before resolving the blocker. Empty
    claims can mean caught up, delayed retries or full lease slots. Requires the existing claim
    permission. Tokens are returned once; no response caching or automatic claim retries.

    Args:
        group_id (str):
        body (RealtimeWorkerBatchClaimRequest): Realtime Worker Batch Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeConsumerGroupBatch
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
    body: RealtimeWorkerBatchClaimRequest,
) -> Response[Problem | RealtimeConsumerGroupBatch]:
    """Claim a bounded batch with independent leases using scoped worker credentials

     Claim a bounded batch with independent leases. Claim 1–16 messages in one transaction, bounded by
    the group-wide 32 active leases and 128 outstanding snapshots. Each message has its own token and
    settlement. Always returns 200 with a possibly empty claims array. Partial batches may report
    history_unavailable or capacity_blocked; process returned claims before resolving the blocker. Empty
    claims can mean caught up, delayed retries or full lease slots. Requires the existing claim
    permission. Tokens are returned once; no response caching or automatic claim retries.

    Args:
        group_id (str):
        body (RealtimeWorkerBatchClaimRequest): Realtime Worker Batch Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeConsumerGroupBatch]
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
    body: RealtimeWorkerBatchClaimRequest,
) -> Problem | RealtimeConsumerGroupBatch | None:
    """Claim a bounded batch with independent leases using scoped worker credentials

     Claim a bounded batch with independent leases. Claim 1–16 messages in one transaction, bounded by
    the group-wide 32 active leases and 128 outstanding snapshots. Each message has its own token and
    settlement. Always returns 200 with a possibly empty claims array. Partial batches may report
    history_unavailable or capacity_blocked; process returned claims before resolving the blocker. Empty
    claims can mean caught up, delayed retries or full lease slots. Requires the existing claim
    permission. Tokens are returned once; no response caching or automatic claim retries.

    Args:
        group_id (str):
        body (RealtimeWorkerBatchClaimRequest): Realtime Worker Batch Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeConsumerGroupBatch
    """

    return (
        await asyncio_detailed(
            group_id=group_id,
            client=client,
            body=body,
        )
    ).parsed
