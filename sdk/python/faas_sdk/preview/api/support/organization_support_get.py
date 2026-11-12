from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.support_ticket import SupportTicket
from ...types import Response


def _get_kwargs(
    org: str,
    ticket: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{org}/support/tickets/{ticket}".format(
            org=quote(str(org), safe=""),
            ticket=quote(str(ticket), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SupportTicket | None:
    if response.status_code == 200:
        response_200 = SupportTicket.from_dict(response.json())

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
) -> Response[Problem | SupportTicket]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | SupportTicket]:
    """Get organization support case workflow

     Organization support get operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        org (str):
        ticket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportTicket]
    """

    kwargs = _get_kwargs(
        org=org,
        ticket=ticket,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | SupportTicket | None:
    """Get organization support case workflow

     Organization support get operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        org (str):
        ticket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportTicket
    """

    return sync_detailed(
        org=org,
        ticket=ticket,
        client=client,
    ).parsed


async def asyncio_detailed(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | SupportTicket]:
    """Get organization support case workflow

     Organization support get operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        org (str):
        ticket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportTicket]
    """

    kwargs = _get_kwargs(
        org=org,
        ticket=ticket,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | SupportTicket | None:
    """Get organization support case workflow

     Organization support get operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        org (str):
        ticket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportTicket
    """

    return (
        await asyncio_detailed(
            org=org,
            ticket=ticket,
            client=client,
        )
    ).parsed
