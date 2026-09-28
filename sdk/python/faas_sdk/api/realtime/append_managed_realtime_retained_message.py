from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_retained_message_request import ManagedRealtimeRetainedMessageRequest
from ...models.managed_realtime_retained_message_response import ManagedRealtimeRetainedMessageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    *,
    body: ManagedRealtimeRetainedMessageRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages".format(
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
) -> ManagedRealtimeRetainedMessageResponse | Problem | None:
    if response.status_code == 201:
        response_201 = ManagedRealtimeRetainedMessageResponse.from_dict(response.json())

        return response_201

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
) -> Response[ManagedRealtimeRetainedMessageResponse | Problem]:
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
    body: ManagedRealtimeRetainedMessageRequest,
) -> Response[ManagedRealtimeRetainedMessageResponse | Problem]:
    """Append one ordered message to retained channel history.

     This storage preview does not deliver to WebSocket clients. The sequence is committed before the
    response; idempotency applies while the message remains retained. At most 32 channels and 1024
    messages per channel are retained per endpoint, for up to 24 hours.

    Args:
        slug (str):
        id (str):
        channel (str):
        body (ManagedRealtimeRetainedMessageRequest): Binary-safe payload to append to retained
            channel history.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeRetainedMessageResponse | Problem]
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
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeRetainedMessageRequest,
) -> ManagedRealtimeRetainedMessageResponse | Problem | None:
    """Append one ordered message to retained channel history.

     This storage preview does not deliver to WebSocket clients. The sequence is committed before the
    response; idempotency applies while the message remains retained. At most 32 channels and 1024
    messages per channel are retained per endpoint, for up to 24 hours.

    Args:
        slug (str):
        id (str):
        channel (str):
        body (ManagedRealtimeRetainedMessageRequest): Binary-safe payload to append to retained
            channel history.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeRetainedMessageResponse | Problem
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
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeRetainedMessageRequest,
) -> Response[ManagedRealtimeRetainedMessageResponse | Problem]:
    """Append one ordered message to retained channel history.

     This storage preview does not deliver to WebSocket clients. The sequence is committed before the
    response; idempotency applies while the message remains retained. At most 32 channels and 1024
    messages per channel are retained per endpoint, for up to 24 hours.

    Args:
        slug (str):
        id (str):
        channel (str):
        body (ManagedRealtimeRetainedMessageRequest): Binary-safe payload to append to retained
            channel history.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeRetainedMessageResponse | Problem]
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
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeRetainedMessageRequest,
) -> ManagedRealtimeRetainedMessageResponse | Problem | None:
    """Append one ordered message to retained channel history.

     This storage preview does not deliver to WebSocket clients. The sequence is committed before the
    response; idempotency applies while the message remains retained. At most 32 channels and 1024
    messages per channel are retained per endpoint, for up to 24 hours.

    Args:
        slug (str):
        id (str):
        channel (str):
        body (ManagedRealtimeRetainedMessageRequest): Binary-safe payload to append to retained
            channel history.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeRetainedMessageResponse | Problem
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
