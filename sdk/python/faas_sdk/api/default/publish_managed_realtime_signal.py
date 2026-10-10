from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_signal_request import ManagedRealtimeSignalRequest
from ...models.managed_realtime_signal_response import ManagedRealtimeSignalResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    *,
    body: ManagedRealtimeSignalRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/signals".format(
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
) -> ManagedRealtimeSignalResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeSignalResponse.from_dict(response.json())

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
) -> Response[ManagedRealtimeSignalResponse | Problem]:
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
    body: ManagedRealtimeSignalRequest,
) -> Response[ManagedRealtimeSignalResponse | Problem]:
    """Send an ephemeral backend signal to connected v2 channel subscribers

     Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender
    member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit;
    request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per
    API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0
    clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber
    acknowledgement; failures may follow partial delivery.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeSignalRequest): Ephemeral backend signal payload with optional name
            and expiration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeSignalResponse | Problem]
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
    body: ManagedRealtimeSignalRequest,
) -> ManagedRealtimeSignalResponse | Problem | None:
    """Send an ephemeral backend signal to connected v2 channel subscribers

     Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender
    member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit;
    request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per
    API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0
    clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber
    acknowledgement; failures may follow partial delivery.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeSignalRequest): Ephemeral backend signal payload with optional name
            and expiration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeSignalResponse | Problem
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
    body: ManagedRealtimeSignalRequest,
) -> Response[ManagedRealtimeSignalResponse | Problem]:
    """Send an ephemeral backend signal to connected v2 channel subscribers

     Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender
    member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit;
    request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per
    API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0
    clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber
    acknowledgement; failures may follow partial delivery.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeSignalRequest): Ephemeral backend signal payload with optional name
            and expiration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeSignalResponse | Problem]
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
    body: ManagedRealtimeSignalRequest,
) -> ManagedRealtimeSignalResponse | Problem | None:
    """Send an ephemeral backend signal to connected v2 channel subscribers

     Best-effort live delivery with no retained history, sequence, replay, or idempotency receipt. Sender
    member_id is backend. JSON data is limited to 2048 encoded bytes and the endpoint payload limit;
    request body is limited to 4096 bytes. Rate limit is 20 requests per second per endpoint/channel per
    API process, in addition to normal auth limits. Named signals default to a 5000 ms expiry; ttl_ms 0
    clears a named signal. Deploy-write scopes and MFA apply. Acceptance is not subscriber
    acknowledgement; failures may follow partial delivery.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (ManagedRealtimeSignalRequest): Ephemeral backend signal payload with optional name
            and expiration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeSignalResponse | Problem
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
