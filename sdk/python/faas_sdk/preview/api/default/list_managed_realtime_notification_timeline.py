from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_notification_timeline_event import ManagedRealtimeNotificationTimelineEvent
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    principal: str,
    before: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["principal"] = principal

    params["before"] = before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}/timeline".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            message_id=quote(str(message_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | list[ManagedRealtimeNotificationTimelineEvent] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = ManagedRealtimeNotificationTimelineEvent.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | list[ManagedRealtimeNotificationTimelineEvent]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
    before: int | Unset = UNSET,
) -> Response[Any | list[ManagedRealtimeNotificationTimelineEvent]]:
    """List notification state changes across devices, newest first

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        before (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | list[ManagedRealtimeNotificationTimelineEvent]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
        principal=principal,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
    before: int | Unset = UNSET,
) -> Any | list[ManagedRealtimeNotificationTimelineEvent] | None:
    """List notification state changes across devices, newest first

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        before (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | list[ManagedRealtimeNotificationTimelineEvent]
    """

    return sync_detailed(
        slug=slug,
        id=id,
        message_id=message_id,
        client=client,
        principal=principal,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
    before: int | Unset = UNSET,
) -> Response[Any | list[ManagedRealtimeNotificationTimelineEvent]]:
    """List notification state changes across devices, newest first

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        before (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | list[ManagedRealtimeNotificationTimelineEvent]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
        principal=principal,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
    before: int | Unset = UNSET,
) -> Any | list[ManagedRealtimeNotificationTimelineEvent] | None:
    """List notification state changes across devices, newest first

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        before (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | list[ManagedRealtimeNotificationTimelineEvent]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            message_id=message_id,
            client=client,
            principal=principal,
            before=before,
        )
    ).parsed
