from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.support_message_page import SupportMessagePage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    ticket: UUID,
    *,
    after: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after"] = after

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/support/tickets/{ticket}/messages".format(
            ticket=quote(str(ticket), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SupportMessagePage | None:
    if response.status_code == 200:
        response_200 = SupportMessagePage.from_dict(response.json())

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
) -> Response[Problem | SupportMessagePage]:
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
    after: int | Unset = UNSET,
) -> Response[Problem | SupportMessagePage]:
    """Messages personal support case workflow

     Personal support messages operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        after (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportMessagePage]
    """

    kwargs = _get_kwargs(
        ticket=ticket,
        after=after,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = UNSET,
) -> Problem | SupportMessagePage | None:
    """Messages personal support case workflow

     Personal support messages operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        after (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportMessagePage
    """

    return sync_detailed(
        ticket=ticket,
        client=client,
        after=after,
    ).parsed


async def asyncio_detailed(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = UNSET,
) -> Response[Problem | SupportMessagePage]:
    """Messages personal support case workflow

     Personal support messages operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        after (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportMessagePage]
    """

    kwargs = _get_kwargs(
        ticket=ticket,
        after=after,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = UNSET,
) -> Problem | SupportMessagePage | None:
    """Messages personal support case workflow

     Personal support messages operation. Responses omit private notes, staff queues and engineering
    details. Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        ticket (UUID):
        after (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportMessagePage
    """

    return (
        await asyncio_detailed(
            ticket=ticket,
            client=client,
            after=after,
        )
    ).parsed
