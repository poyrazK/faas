from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_channel_batch_request import ManagedRealtimeChannelBatchRequest
from ...models.managed_realtime_channel_batch_response import ManagedRealtimeChannelBatchResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    *,
    body: ManagedRealtimeChannelBatchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish-batch".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ManagedRealtimeChannelBatchResponse | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeChannelBatchResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

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
) -> Response[Any | ManagedRealtimeChannelBatchResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeChannelBatchRequest,
) -> Response[Any | ManagedRealtimeChannelBatchResponse]:
    """Atomically retain a bounded batch, then attempt live delivery

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeChannelBatchRequest): Atomic batch of retained channel messages with
            an optional sequence precondition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeChannelBatchResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeChannelBatchRequest,
) -> Any | ManagedRealtimeChannelBatchResponse | None:
    """Atomically retain a bounded batch, then attempt live delivery

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeChannelBatchRequest): Atomic batch of retained channel messages with
            an optional sequence precondition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeChannelBatchResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeChannelBatchRequest,
) -> Response[Any | ManagedRealtimeChannelBatchResponse]:
    """Atomically retain a bounded batch, then attempt live delivery

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeChannelBatchRequest): Atomic batch of retained channel messages with
            an optional sequence precondition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeChannelBatchResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeChannelBatchRequest,
) -> Any | ManagedRealtimeChannelBatchResponse | None:
    """Atomically retain a bounded batch, then attempt live delivery

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeChannelBatchRequest): Atomic batch of retained channel messages with
            an optional sequence precondition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeChannelBatchResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
            body=body,
        )
    ).parsed
