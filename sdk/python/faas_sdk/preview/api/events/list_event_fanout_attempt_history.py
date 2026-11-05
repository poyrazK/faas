from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_fanout_attempt_history_response import EventFanoutAttemptHistoryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    event_source: str,
    event_id: str,
    subscription_id: UUID | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["event_source"] = event_source

    params["event_id"] = event_id

    json_subscription_id: str | Unset = UNSET
    if not isinstance(subscription_id, Unset):
        json_subscription_id = str(subscription_id)
    params["subscription_id"] = json_subscription_id

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-deliveries/attempts".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventFanoutAttemptHistoryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventFanoutAttemptHistoryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventFanoutAttemptHistoryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str,
    event_id: str,
    subscription_id: UUID | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[EventFanoutAttemptHistoryResponse | Problem]:
    """Inspect one event recipient's fanout attempt history.

     Returns the routing outcomes and explicit operator replay requests
    retained for one app-scoped event identity. This immutable history is
    separate from the current recipient checkpoint and survives replay.
    Event source and ID are required so reused IDs cannot mix histories.

    Args:
        slug (str):
        event_source (str):
        event_id (str):
        subscription_id (UUID | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventFanoutAttemptHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        event_source=event_source,
        event_id=event_id,
        subscription_id=subscription_id,
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str,
    event_id: str,
    subscription_id: UUID | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> EventFanoutAttemptHistoryResponse | Problem | None:
    """Inspect one event recipient's fanout attempt history.

     Returns the routing outcomes and explicit operator replay requests
    retained for one app-scoped event identity. This immutable history is
    separate from the current recipient checkpoint and survives replay.
    Event source and ID are required so reused IDs cannot mix histories.

    Args:
        slug (str):
        event_source (str):
        event_id (str):
        subscription_id (UUID | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventFanoutAttemptHistoryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        event_source=event_source,
        event_id=event_id,
        subscription_id=subscription_id,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str,
    event_id: str,
    subscription_id: UUID | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[EventFanoutAttemptHistoryResponse | Problem]:
    """Inspect one event recipient's fanout attempt history.

     Returns the routing outcomes and explicit operator replay requests
    retained for one app-scoped event identity. This immutable history is
    separate from the current recipient checkpoint and survives replay.
    Event source and ID are required so reused IDs cannot mix histories.

    Args:
        slug (str):
        event_source (str):
        event_id (str):
        subscription_id (UUID | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventFanoutAttemptHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        event_source=event_source,
        event_id=event_id,
        subscription_id=subscription_id,
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str,
    event_id: str,
    subscription_id: UUID | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> EventFanoutAttemptHistoryResponse | Problem | None:
    """Inspect one event recipient's fanout attempt history.

     Returns the routing outcomes and explicit operator replay requests
    retained for one app-scoped event identity. This immutable history is
    separate from the current recipient checkpoint and survives replay.
    Event source and ID are required so reused IDs cannot mix histories.

    Args:
        slug (str):
        event_source (str):
        event_id (str):
        subscription_id (UUID | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventFanoutAttemptHistoryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            event_source=event_source,
            event_id=event_id,
            subscription_id=subscription_id,
            before=before,
            limit=limit,
        )
    ).parsed
