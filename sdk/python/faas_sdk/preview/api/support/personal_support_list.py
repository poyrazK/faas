from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.personal_support_list_status import PersonalSupportListStatus
from ...models.problem import Problem
from ...models.support_ticket_page import SupportTicketPage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    cursor: UUID | Unset = UNSET,
    status: PersonalSupportListStatus | Unset = UNSET,
    search: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_cursor: str | Unset = UNSET
    if not isinstance(cursor, Unset):
        json_cursor = str(cursor)
    params["cursor"] = json_cursor

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["search"] = search

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/support/tickets",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SupportTicketPage | None:
    if response.status_code == 200:
        response_200 = SupportTicketPage.from_dict(response.json())

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
) -> Response[Problem | SupportTicketPage]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    cursor: UUID | Unset = UNSET,
    status: PersonalSupportListStatus | Unset = UNSET,
    search: str | Unset = UNSET,
) -> Response[Problem | SupportTicketPage]:
    """List personal support cases

     Personal support list operation. Responses omit private notes, staff queues and engineering details.
    Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        cursor (UUID | Unset):
        status (PersonalSupportListStatus | Unset):
        search (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportTicketPage]
    """

    kwargs = _get_kwargs(
        cursor=cursor,
        status=status,
        search=search,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    cursor: UUID | Unset = UNSET,
    status: PersonalSupportListStatus | Unset = UNSET,
    search: str | Unset = UNSET,
) -> Problem | SupportTicketPage | None:
    """List personal support cases

     Personal support list operation. Responses omit private notes, staff queues and engineering details.
    Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        cursor (UUID | Unset):
        status (PersonalSupportListStatus | Unset):
        search (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportTicketPage
    """

    return sync_detailed(
        client=client,
        cursor=cursor,
        status=status,
        search=search,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    cursor: UUID | Unset = UNSET,
    status: PersonalSupportListStatus | Unset = UNSET,
    search: str | Unset = UNSET,
) -> Response[Problem | SupportTicketPage]:
    """List personal support cases

     Personal support list operation. Responses omit private notes, staff queues and engineering details.
    Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        cursor (UUID | Unset):
        status (PersonalSupportListStatus | Unset):
        search (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportTicketPage]
    """

    kwargs = _get_kwargs(
        cursor=cursor,
        status=status,
        search=search,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    cursor: UUID | Unset = UNSET,
    status: PersonalSupportListStatus | Unset = UNSET,
    search: str | Unset = UNSET,
) -> Problem | SupportTicketPage | None:
    """List personal support cases

     Personal support list operation. Responses omit private notes, staff queues and engineering details.
    Writes require current verified access, the expected revision where applicable, and a UUID
    idempotency key.

    Args:
        cursor (UUID | Unset):
        status (PersonalSupportListStatus | Unset):
        search (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportTicketPage
    """

    return (
        await asyncio_detailed(
            client=client,
            cursor=cursor,
            status=status,
            search=search,
        )
    ).parsed
