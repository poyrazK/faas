from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_claim import RealtimeConsumerGroupClaim
from ...models.realtime_consumer_group_claim_request import RealtimeConsumerGroupClaimRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeConsumerGroupClaimRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims".format(
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
) -> Any | Problem | RealtimeConsumerGroupClaim | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupClaim.from_dict(response.json())

        return response_200

    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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
) -> Response[Any | Problem | RealtimeConsumerGroupClaim]:
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
    body: RealtimeConsumerGroupClaimRequest,
) -> Response[Any | Problem | RealtimeConsumerGroupClaim]:
    """Claim one retained message with an exclusive short lease

     Returns 204 when paused, caught up, retry-delayed, or all active lease slots are occupied. Returns
    429 when the outstanding snapshot cap prevents assignment, and 409 on a retention gap. Claim tokens
    are returned once and hashed at rest; this operation is excluded from generic response caching.
    Lease expiry may permit duplicate processing; use idempotent handlers. Does not accept subscription
    tickets or WebSocket credentials.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupClaimRequest): Realtime Consumer Group Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupClaim]
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
    body: RealtimeConsumerGroupClaimRequest,
) -> Any | Problem | RealtimeConsumerGroupClaim | None:
    """Claim one retained message with an exclusive short lease

     Returns 204 when paused, caught up, retry-delayed, or all active lease slots are occupied. Returns
    429 when the outstanding snapshot cap prevents assignment, and 409 on a retention gap. Claim tokens
    are returned once and hashed at rest; this operation is excluded from generic response caching.
    Lease expiry may permit duplicate processing; use idempotent handlers. Does not accept subscription
    tickets or WebSocket credentials.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupClaimRequest): Realtime Consumer Group Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupClaim
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
    body: RealtimeConsumerGroupClaimRequest,
) -> Response[Any | Problem | RealtimeConsumerGroupClaim]:
    """Claim one retained message with an exclusive short lease

     Returns 204 when paused, caught up, retry-delayed, or all active lease slots are occupied. Returns
    429 when the outstanding snapshot cap prevents assignment, and 409 on a retention gap. Claim tokens
    are returned once and hashed at rest; this operation is excluded from generic response caching.
    Lease expiry may permit duplicate processing; use idempotent handlers. Does not accept subscription
    tickets or WebSocket credentials.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupClaimRequest): Realtime Consumer Group Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupClaim]
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
    body: RealtimeConsumerGroupClaimRequest,
) -> Any | Problem | RealtimeConsumerGroupClaim | None:
    """Claim one retained message with an exclusive short lease

     Returns 204 when paused, caught up, retry-delayed, or all active lease slots are occupied. Returns
    429 when the outstanding snapshot cap prevents assignment, and 409 on a retention gap. Claim tokens
    are returned once and hashed at rest; this operation is excluded from generic response caching.
    Lease expiry may permit duplicate processing; use idempotent handlers. Does not accept subscription
    tickets or WebSocket credentials.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupClaimRequest): Realtime Consumer Group Claim Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupClaim
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
