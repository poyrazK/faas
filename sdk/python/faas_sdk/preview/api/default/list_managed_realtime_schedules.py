from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_managed_realtime_schedules_status import (
    ListManagedRealtimeSchedulesStatus,
)
from ...models.managed_realtime_schedules_response import ManagedRealtimeSchedulesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    *,
    group: str | Unset = UNSET,
    status: ListManagedRealtimeSchedulesStatus | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["group"] = group

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeSchedulesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeSchedulesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedRealtimeSchedulesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    group: str | Unset = UNSET,
    status: ListManagedRealtimeSchedulesStatus | Unset = UNSET,
) -> Response[ManagedRealtimeSchedulesResponse | Problem]:
    """List pending and recent terminal retained-event schedules

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str | Unset):
        status (ListManagedRealtimeSchedulesStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeSchedulesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        status=status,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    group: str | Unset = UNSET,
    status: ListManagedRealtimeSchedulesStatus | Unset = UNSET,
) -> ManagedRealtimeSchedulesResponse | Problem | None:
    """List pending and recent terminal retained-event schedules

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str | Unset):
        status (ListManagedRealtimeSchedulesStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeSchedulesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
        group=group,
        status=status,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    group: str | Unset = UNSET,
    status: ListManagedRealtimeSchedulesStatus | Unset = UNSET,
) -> Response[ManagedRealtimeSchedulesResponse | Problem]:
    """List pending and recent terminal retained-event schedules

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str | Unset):
        status (ListManagedRealtimeSchedulesStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeSchedulesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        status=status,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    group: str | Unset = UNSET,
    status: ListManagedRealtimeSchedulesStatus | Unset = UNSET,
) -> ManagedRealtimeSchedulesResponse | Problem | None:
    """List pending and recent terminal retained-event schedules

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str | Unset):
        status (ListManagedRealtimeSchedulesStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeSchedulesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
            group=group,
            status=status,
        )
    ).parsed
