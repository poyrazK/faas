from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_events_response import OperationEventsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    after: int | Unset = UNSET,
    last_event_id: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(last_event_id, Unset):
        headers["Last-Event-ID"] = last_event_id

    params: dict[str, Any] = {}

    params["after"] = after

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/customer-operations/{id}/events".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationEventsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationEventsResponse.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[OperationEventsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = UNSET,
    last_event_id: str | Unset = UNSET,
) -> Response[OperationEventsResponse | Problem]:
    """Read or subscribe to durable operation progress.

     Requires platform_tenant:operations:read. application/json returns a bounded ordered page. Accept:
    text/event-stream streams durable events plus snapshot/resync frames; reconnect at least every five
    minutes with a current credential. after overrides Last-Event-ID. Authorization is rechecked during
    streams; revocation and suspension close them. When history expired or the cursor is ahead, a resync
    frame instructs a fresh status read. Event retention is Hobby/Pro/Scale 1/7/30 days; PostgreSQL
    notifications are only wake hints.

    Args:
        id (UUID):
        after (int | Unset):
        last_event_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        after=after,
        last_event_id=last_event_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = UNSET,
    last_event_id: str | Unset = UNSET,
) -> OperationEventsResponse | Problem | None:
    """Read or subscribe to durable operation progress.

     Requires platform_tenant:operations:read. application/json returns a bounded ordered page. Accept:
    text/event-stream streams durable events plus snapshot/resync frames; reconnect at least every five
    minutes with a current credential. after overrides Last-Event-ID. Authorization is rechecked during
    streams; revocation and suspension close them. When history expired or the cursor is ahead, a resync
    frame instructs a fresh status read. Event retention is Hobby/Pro/Scale 1/7/30 days; PostgreSQL
    notifications are only wake hints.

    Args:
        id (UUID):
        after (int | Unset):
        last_event_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationEventsResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        after=after,
        last_event_id=last_event_id,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = UNSET,
    last_event_id: str | Unset = UNSET,
) -> Response[OperationEventsResponse | Problem]:
    """Read or subscribe to durable operation progress.

     Requires platform_tenant:operations:read. application/json returns a bounded ordered page. Accept:
    text/event-stream streams durable events plus snapshot/resync frames; reconnect at least every five
    minutes with a current credential. after overrides Last-Event-ID. Authorization is rechecked during
    streams; revocation and suspension close them. When history expired or the cursor is ahead, a resync
    frame instructs a fresh status read. Event retention is Hobby/Pro/Scale 1/7/30 days; PostgreSQL
    notifications are only wake hints.

    Args:
        id (UUID):
        after (int | Unset):
        last_event_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        after=after,
        last_event_id=last_event_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = UNSET,
    last_event_id: str | Unset = UNSET,
) -> OperationEventsResponse | Problem | None:
    """Read or subscribe to durable operation progress.

     Requires platform_tenant:operations:read. application/json returns a bounded ordered page. Accept:
    text/event-stream streams durable events plus snapshot/resync frames; reconnect at least every five
    minutes with a current credential. after overrides Last-Event-ID. Authorization is rechecked during
    streams; revocation and suspension close them. When history expired or the cursor is ahead, a resync
    frame instructs a fresh status read. Event retention is Hobby/Pro/Scale 1/7/30 days; PostgreSQL
    notifications are only wake hints.

    Args:
        id (UUID):
        after (int | Unset):
        last_event_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            after=after,
            last_event_id=last_event_id,
        )
    ).parsed
