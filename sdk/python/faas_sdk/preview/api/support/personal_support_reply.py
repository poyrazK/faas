from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.support_message_request import SupportMessageRequest
from ...models.support_submission import SupportSubmission
from ...types import Response


def _get_kwargs(
    ticket: UUID,
    *,
    body: SupportMessageRequest,
    idempotency_key: UUID,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/support/tickets/{ticket}/messages".format(
            ticket=quote(str(ticket), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SupportSubmission | None:
    if response.status_code == 202:
        response_202 = SupportSubmission.from_dict(response.json())

        return response_202

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[Problem | SupportSubmission]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportMessageRequest,
    idempotency_key: UUID,
) -> Response[Problem | SupportSubmission]:
    """Reply personal support case workflow

     Personal support reply operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        idempotency_key (UUID):
        body (SupportMessageRequest): Append one customer reply to an unresolved case at the
            expected revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportSubmission]
    """

    kwargs = _get_kwargs(
        ticket=ticket,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportMessageRequest,
    idempotency_key: UUID,
) -> Problem | SupportSubmission | None:
    """Reply personal support case workflow

     Personal support reply operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        idempotency_key (UUID):
        body (SupportMessageRequest): Append one customer reply to an unresolved case at the
            expected revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportSubmission
    """

    return sync_detailed(
        ticket=ticket,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportMessageRequest,
    idempotency_key: UUID,
) -> Response[Problem | SupportSubmission]:
    """Reply personal support case workflow

     Personal support reply operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        idempotency_key (UUID):
        body (SupportMessageRequest): Append one customer reply to an unresolved case at the
            expected revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportSubmission]
    """

    kwargs = _get_kwargs(
        ticket=ticket,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportMessageRequest,
    idempotency_key: UUID,
) -> Problem | SupportSubmission | None:
    """Reply personal support case workflow

     Personal support reply operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        idempotency_key (UUID):
        body (SupportMessageRequest): Append one customer reply to an unresolved case at the
            expected revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportSubmission
    """

    return (
        await asyncio_detailed(
            ticket=ticket,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
