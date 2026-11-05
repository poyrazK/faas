from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recipient_blocker_batch_request import EventRecipientBlockerBatchRequest
from ...models.event_recipient_blocker_batch_response import EventRecipientBlockerBatchResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: EventRecipientBlockerBatchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/events/recipient-blockers",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecipientBlockerBatchResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecipientBlockerBatchResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRecipientBlockerBatchResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: EventRecipientBlockerBatchRequest,
) -> Response[EventRecipientBlockerBatchResponse | Problem]:
    """Read an ordered batch of exact recipient routing diagnostics.

     Read-only. One independent snapshot per item, up to 50 items and 65536 body bytes. Duplicate queries
    are processed independently. All guards validate before reads. Results preserve request order and
    echo each query. Per-item read failures contain no evidence. A shared five second deadline bounds
    work; remaining items return read_timeout. No atomic batch snapshot or handler completion guarantee.

    Args:
        body (EventRecipientBlockerBatchRequest): Bounded exact-recipient queries, each guarded by
            the original acceptance instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecipientBlockerBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: EventRecipientBlockerBatchRequest,
) -> EventRecipientBlockerBatchResponse | Problem | None:
    """Read an ordered batch of exact recipient routing diagnostics.

     Read-only. One independent snapshot per item, up to 50 items and 65536 body bytes. Duplicate queries
    are processed independently. All guards validate before reads. Results preserve request order and
    echo each query. Per-item read failures contain no evidence. A shared five second deadline bounds
    work; remaining items return read_timeout. No atomic batch snapshot or handler completion guarantee.

    Args:
        body (EventRecipientBlockerBatchRequest): Bounded exact-recipient queries, each guarded by
            the original acceptance instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecipientBlockerBatchResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: EventRecipientBlockerBatchRequest,
) -> Response[EventRecipientBlockerBatchResponse | Problem]:
    """Read an ordered batch of exact recipient routing diagnostics.

     Read-only. One independent snapshot per item, up to 50 items and 65536 body bytes. Duplicate queries
    are processed independently. All guards validate before reads. Results preserve request order and
    echo each query. Per-item read failures contain no evidence. A shared five second deadline bounds
    work; remaining items return read_timeout. No atomic batch snapshot or handler completion guarantee.

    Args:
        body (EventRecipientBlockerBatchRequest): Bounded exact-recipient queries, each guarded by
            the original acceptance instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecipientBlockerBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: EventRecipientBlockerBatchRequest,
) -> EventRecipientBlockerBatchResponse | Problem | None:
    """Read an ordered batch of exact recipient routing diagnostics.

     Read-only. One independent snapshot per item, up to 50 items and 65536 body bytes. Duplicate queries
    are processed independently. All guards validate before reads. Results preserve request order and
    echo each query. Per-item read failures contain no evidence. A shared five second deadline bounds
    work; remaining items return read_timeout. No atomic batch snapshot or handler completion guarantee.

    Args:
        body (EventRecipientBlockerBatchRequest): Bounded exact-recipient queries, each guarded by
            the original acceptance instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecipientBlockerBatchResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
