from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_publisher_message_request import RealtimePublisherMessageRequest
from ...models.realtime_publisher_publish_response import RealtimePublisherPublishResponse
from ...types import Response


def _get_kwargs(
    endpoint_id: str,
    channel: str,
    *,
    body: RealtimePublisherMessageRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish".format(
            endpoint_id=quote(str(endpoint_id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RealtimePublisherPublishResponse | None:
    if response.status_code == 200:
        response_200 = RealtimePublisherPublishResponse.from_dict(response.json())

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
) -> Response[Problem | RealtimePublisherPublishResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    endpoint_id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
    body: RealtimePublisherMessageRequest,
    idempotency_key: str,
) -> Response[Problem | RealtimePublisherPublishResponse]:
    """Publish a retained channel event with a scoped credential

     Accepts only an endpoint-bound grp_ credential. Retained publication only; exact channel/event
    grants, decoded payload size, fixed-minute accepted-publish quota and idempotency receipt commit
    atomically. Matching retries within 24 hours return the original sequence without re-emitting or
    consuming quota; rotated credentials retain their receipt namespace. Requires history preview and
    live realtime plan access.

    Args:
        endpoint_id (str):
        channel (str):
        idempotency_key (str):
        body (RealtimePublisherMessageRequest): Realtime Publisher Message Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimePublisherPublishResponse]
    """

    kwargs = _get_kwargs(
        endpoint_id=endpoint_id,
        channel=channel,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    endpoint_id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
    body: RealtimePublisherMessageRequest,
    idempotency_key: str,
) -> Problem | RealtimePublisherPublishResponse | None:
    """Publish a retained channel event with a scoped credential

     Accepts only an endpoint-bound grp_ credential. Retained publication only; exact channel/event
    grants, decoded payload size, fixed-minute accepted-publish quota and idempotency receipt commit
    atomically. Matching retries within 24 hours return the original sequence without re-emitting or
    consuming quota; rotated credentials retain their receipt namespace. Requires history preview and
    live realtime plan access.

    Args:
        endpoint_id (str):
        channel (str):
        idempotency_key (str):
        body (RealtimePublisherMessageRequest): Realtime Publisher Message Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimePublisherPublishResponse
    """

    return sync_detailed(
        endpoint_id=endpoint_id,
        channel=channel,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    endpoint_id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
    body: RealtimePublisherMessageRequest,
    idempotency_key: str,
) -> Response[Problem | RealtimePublisherPublishResponse]:
    """Publish a retained channel event with a scoped credential

     Accepts only an endpoint-bound grp_ credential. Retained publication only; exact channel/event
    grants, decoded payload size, fixed-minute accepted-publish quota and idempotency receipt commit
    atomically. Matching retries within 24 hours return the original sequence without re-emitting or
    consuming quota; rotated credentials retain their receipt namespace. Requires history preview and
    live realtime plan access.

    Args:
        endpoint_id (str):
        channel (str):
        idempotency_key (str):
        body (RealtimePublisherMessageRequest): Realtime Publisher Message Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimePublisherPublishResponse]
    """

    kwargs = _get_kwargs(
        endpoint_id=endpoint_id,
        channel=channel,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    endpoint_id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
    body: RealtimePublisherMessageRequest,
    idempotency_key: str,
) -> Problem | RealtimePublisherPublishResponse | None:
    """Publish a retained channel event with a scoped credential

     Accepts only an endpoint-bound grp_ credential. Retained publication only; exact channel/event
    grants, decoded payload size, fixed-minute accepted-publish quota and idempotency receipt commit
    atomically. Matching retries within 24 hours return the original sequence without re-emitting or
    consuming quota; rotated credentials retain their receipt namespace. Requires history preview and
    live realtime plan access.

    Args:
        endpoint_id (str):
        channel (str):
        idempotency_key (str):
        body (RealtimePublisherMessageRequest): Realtime Publisher Message Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimePublisherPublishResponse
    """

    return (
        await asyncio_detailed(
            endpoint_id=endpoint_id,
            channel=channel,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
