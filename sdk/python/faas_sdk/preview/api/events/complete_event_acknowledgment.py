from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_acknowledgment_request import EventAcknowledgmentRequest
from ...models.event_acknowledgment_status import EventAcknowledgmentStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    invocation_id: UUID,
    *,
    body: EventAcknowledgmentRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-deliveries/{invocation_id}/acknowledgment/complete".format(
            slug=quote(str(slug), safe=""),
            invocation_id=quote(str(invocation_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventAcknowledgmentStatus | Problem | None:
    if response.status_code == 200:
        response_200 = EventAcknowledgmentStatus.from_dict(response.json())

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

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventAcknowledgmentStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventAcknowledgmentRequest,
) -> Response[EventAcknowledgmentStatus | Problem]:
    """Complete a deferred delivery attempt.

     Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require
    the opaque token. Never log tokens. Complete only after durable business processing. Matching
    successful repeats return the saved completion state. Replaced attempts and changed results return
    409. Completion can precede the scheduler observing 202.

    Args:
        slug (str):
        invocation_id (UUID):
        body (EventAcknowledgmentRequest): App-authorized callback fenced by the current opaque
            delivery token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        invocation_id=invocation_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventAcknowledgmentRequest,
) -> EventAcknowledgmentStatus | Problem | None:
    """Complete a deferred delivery attempt.

     Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require
    the opaque token. Never log tokens. Complete only after durable business processing. Matching
    successful repeats return the saved completion state. Replaced attempts and changed results return
    409. Completion can precede the scheduler observing 202.

    Args:
        slug (str):
        invocation_id (UUID):
        body (EventAcknowledgmentRequest): App-authorized callback fenced by the current opaque
            delivery token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        invocation_id=invocation_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventAcknowledgmentRequest,
) -> Response[EventAcknowledgmentStatus | Problem]:
    """Complete a deferred delivery attempt.

     Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require
    the opaque token. Never log tokens. Complete only after durable business processing. Matching
    successful repeats return the saved completion state. Replaced attempts and changed results return
    409. Completion can precede the scheduler observing 202.

    Args:
        slug (str):
        invocation_id (UUID):
        body (EventAcknowledgmentRequest): App-authorized callback fenced by the current opaque
            delivery token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        invocation_id=invocation_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventAcknowledgmentRequest,
) -> EventAcknowledgmentStatus | Problem | None:
    """Complete a deferred delivery attempt.

     Requires deploy:write or admin. Writes remain app-scoped; delivery callbacks additionally require
    the opaque token. Never log tokens. Complete only after durable business processing. Matching
    successful repeats return the saved completion state. Replaced attempts and changed results return
    409. Completion can precede the scheduler observing 202.

    Args:
        slug (str):
        invocation_id (UUID):
        body (EventAcknowledgmentRequest): App-authorized callback fenced by the current opaque
            delivery token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            invocation_id=invocation_id,
            client=client,
            body=body,
        )
    ).parsed
