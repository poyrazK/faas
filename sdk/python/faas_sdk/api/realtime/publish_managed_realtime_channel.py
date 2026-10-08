from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_message_request import ManagedRealtimeMessageRequest
from ...models.managed_realtime_publish_response import ManagedRealtimePublishResponse
from ...models.problem import Problem
from ...models.publish_managed_realtime_channel_delivery import (
    PublishManagedRealtimeChannelDelivery,
)
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    *,
    body: ManagedRealtimeMessageRequest,
    delivery: PublishManagedRealtimeChannelDelivery | Unset = "live",
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    json_delivery: str | Unset = UNSET
    if not isinstance(delivery, Unset):
        json_delivery = delivery

    params["delivery"] = json_delivery

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimePublishResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimePublishResponse.from_dict(response.json())

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
) -> Response[ManagedRealtimePublishResponse | Problem]:
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
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageRequest,
    delivery: PublishManagedRealtimeChannelDelivery | Unset = "live",
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedRealtimePublishResponse | Problem]:
    """Publish a message to live or resumable channel subscribers.

     delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is preview-
    only and requires FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid; it accepts at most 4096 decoded
    bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel
    log before fan-out and returns its sequence. V2 subscribers read the committed log in sequence; a
    best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log
    remains authoritative if live fan-out is incomplete. The resume protocol separately requires
    FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1 on realtimed.
    A supplied Idempotency-Key binds the publish to delivery mode, decoded payload and binary flag for
    24 hours. Replays return the original response and do not retry recipients that missed a partial
    publish. Reusing a key with a different mode or payload returns 409. An in-flight or uncertain
    reservation also returns 409 and is not run again while the key is active. Queue admission does not
    confirm client receipt.

    Args:
        slug (str):
        id (str):
        channel (str):
        delivery (PublishManagedRealtimeChannelDelivery | Unset):  Default: 'live'.
        idempotency_key (str | Unset):
        body (ManagedRealtimeMessageRequest): Binary-safe message payload encoded as standard
            base64.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimePublishResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        body=body,
        delivery=delivery,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageRequest,
    delivery: PublishManagedRealtimeChannelDelivery | Unset = "live",
    idempotency_key: str | Unset = UNSET,
) -> ManagedRealtimePublishResponse | Problem | None:
    """Publish a message to live or resumable channel subscribers.

     delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is preview-
    only and requires FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid; it accepts at most 4096 decoded
    bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel
    log before fan-out and returns its sequence. V2 subscribers read the committed log in sequence; a
    best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log
    remains authoritative if live fan-out is incomplete. The resume protocol separately requires
    FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1 on realtimed.
    A supplied Idempotency-Key binds the publish to delivery mode, decoded payload and binary flag for
    24 hours. Replays return the original response and do not retry recipients that missed a partial
    publish. Reusing a key with a different mode or payload returns 409. An in-flight or uncertain
    reservation also returns 409 and is not run again while the key is active. Queue admission does not
    confirm client receipt.

    Args:
        slug (str):
        id (str):
        channel (str):
        delivery (PublishManagedRealtimeChannelDelivery | Unset):  Default: 'live'.
        idempotency_key (str | Unset):
        body (ManagedRealtimeMessageRequest): Binary-safe message payload encoded as standard
            base64.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimePublishResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
        body=body,
        delivery=delivery,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageRequest,
    delivery: PublishManagedRealtimeChannelDelivery | Unset = "live",
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedRealtimePublishResponse | Problem]:
    """Publish a message to live or resumable channel subscribers.

     delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is preview-
    only and requires FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid; it accepts at most 4096 decoded
    bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel
    log before fan-out and returns its sequence. V2 subscribers read the committed log in sequence; a
    best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log
    remains authoritative if live fan-out is incomplete. The resume protocol separately requires
    FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1 on realtimed.
    A supplied Idempotency-Key binds the publish to delivery mode, decoded payload and binary flag for
    24 hours. Replays return the original response and do not retry recipients that missed a partial
    publish. Reusing a key with a different mode or payload returns 409. An in-flight or uncertain
    reservation also returns 409 and is not run again while the key is active. Queue admission does not
    confirm client receipt.

    Args:
        slug (str):
        id (str):
        channel (str):
        delivery (PublishManagedRealtimeChannelDelivery | Unset):  Default: 'live'.
        idempotency_key (str | Unset):
        body (ManagedRealtimeMessageRequest): Binary-safe message payload encoded as standard
            base64.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimePublishResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        body=body,
        delivery=delivery,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageRequest,
    delivery: PublishManagedRealtimeChannelDelivery | Unset = "live",
    idempotency_key: str | Unset = UNSET,
) -> ManagedRealtimePublishResponse | Problem | None:
    """Publish a message to live or resumable channel subscribers.

     delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is preview-
    only and requires FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid; it accepts at most 4096 decoded
    bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel
    log before fan-out and returns its sequence. V2 subscribers read the committed log in sequence; a
    best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log
    remains authoritative if live fan-out is incomplete. The resume protocol separately requires
    FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1 on realtimed.
    A supplied Idempotency-Key binds the publish to delivery mode, decoded payload and binary flag for
    24 hours. Replays return the original response and do not retry recipients that missed a partial
    publish. Reusing a key with a different mode or payload returns 409. An in-flight or uncertain
    reservation also returns 409 and is not run again while the key is active. Queue admission does not
    confirm client receipt.

    Args:
        slug (str):
        id (str):
        channel (str):
        delivery (PublishManagedRealtimeChannelDelivery | Unset):  Default: 'live'.
        idempotency_key (str | Unset):
        body (ManagedRealtimeMessageRequest): Binary-safe message payload encoded as standard
            base64.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimePublishResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
            body=body,
            delivery=delivery,
            idempotency_key=idempotency_key,
        )
    ).parsed
