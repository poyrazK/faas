from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_ticket_request import RealtimeTicketRequest
from ...models.realtime_ticket_response import RealtimeTicketResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: RealtimeTicketRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/tickets".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> RealtimeTicketResponse | None:
    if response.status_code == 201:
        response_201 = RealtimeTicketResponse.from_dict(response.json())

        return response_201

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[RealtimeTicketResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeTicketRequest,
) -> Response[RealtimeTicketResponse]:
    """Issue a short-lived channel subscription ticket

     Operator preview. Backend-only app-scoped write with MFA. The backend authorizes the principal and
    explicit channels before issuing. Tickets allow retained subscriptions only and are returned once
    with no-store.

    Args:
        slug (str):
        id (str):
        body (RealtimeTicketRequest): Realtime Ticket Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[RealtimeTicketResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeTicketRequest,
) -> RealtimeTicketResponse | None:
    """Issue a short-lived channel subscription ticket

     Operator preview. Backend-only app-scoped write with MFA. The backend authorizes the principal and
    explicit channels before issuing. Tickets allow retained subscriptions only and are returned once
    with no-store.

    Args:
        slug (str):
        id (str):
        body (RealtimeTicketRequest): Realtime Ticket Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        RealtimeTicketResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeTicketRequest,
) -> Response[RealtimeTicketResponse]:
    """Issue a short-lived channel subscription ticket

     Operator preview. Backend-only app-scoped write with MFA. The backend authorizes the principal and
    explicit channels before issuing. Tickets allow retained subscriptions only and are returned once
    with no-store.

    Args:
        slug (str):
        id (str):
        body (RealtimeTicketRequest): Realtime Ticket Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[RealtimeTicketResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeTicketRequest,
) -> RealtimeTicketResponse | None:
    """Issue a short-lived channel subscription ticket

     Operator preview. Backend-only app-scoped write with MFA. The backend authorizes the principal and
    explicit channels before issuing. Tickets allow retained subscriptions only and are returned once
    with no-store.

    Args:
        slug (str):
        id (str):
        body (RealtimeTicketRequest): Realtime Ticket Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        RealtimeTicketResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
