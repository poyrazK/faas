from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_send_request import QueueSendRequest
from ...models.queue_send_response import QueueSendResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: QueueSendRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/queues/send".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueSendResponse | None:
    if response.status_code == 201:
        response_201 = QueueSendResponse.from_dict(response.json())

        return response_201

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueSendResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | QueueSendResponse]:
    """Enqueue a row on the per-app FIFO queue.

     Cap-checked against the plan's MaxQueueDepth (Hobby 5, Pro 25,
    Scale 100). The drain re-checks at dispatch tick.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendRequest): Body for POST /v1/apps/{slug}/queues/send. Cap-checked against
            MaxQueueDepth.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueSendResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | QueueSendResponse | None:
    """Enqueue a row on the per-app FIFO queue.

     Cap-checked against the plan's MaxQueueDepth (Hobby 5, Pro 25,
    Scale 100). The drain re-checks at dispatch tick.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendRequest): Body for POST /v1/apps/{slug}/queues/send. Cap-checked against
            MaxQueueDepth.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueSendResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | QueueSendResponse]:
    """Enqueue a row on the per-app FIFO queue.

     Cap-checked against the plan's MaxQueueDepth (Hobby 5, Pro 25,
    Scale 100). The drain re-checks at dispatch tick.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendRequest): Body for POST /v1/apps/{slug}/queues/send. Cap-checked against
            MaxQueueDepth.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueSendResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | QueueSendResponse | None:
    """Enqueue a row on the per-app FIFO queue.

     Cap-checked against the plan's MaxQueueDepth (Hobby 5, Pro 25,
    Scale 100). The drain re-checks at dispatch tick.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendRequest): Body for POST /v1/apps/{slug}/queues/send. Cap-checked against
            MaxQueueDepth.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueSendResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
