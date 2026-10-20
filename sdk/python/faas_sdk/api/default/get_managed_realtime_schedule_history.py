from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_schedule_history_response import ManagedRealtimeScheduleHistoryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    after_version: int | Unset = 0,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after_version"] = after_version

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/history".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            schedule_id=quote(str(schedule_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeScheduleHistoryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeScheduleHistoryResponse.from_dict(response.json())

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
) -> Response[ManagedRealtimeScheduleHistoryResponse | Problem]:
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
    schedule_id: str,
    *,
    client: AuthenticatedClient | Client,
    after_version: int | Unset = 0,
    limit: int | Unset = 50,
) -> Response[ManagedRealtimeScheduleHistoryResponse | Problem]:
    """Inspect schedule changes and recorded delivery attempts

     Events commit with schedule changes. Up to 128 entries are retained per
    schedule, ordered by version. history_truncated marks missing earlier
    history, including migrated baseline records. Terminal history expires
    with its schedule receipt. Preview, ownership, MFA, and read scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        after_version (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeScheduleHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        schedule_id=schedule_id,
        after_version=after_version,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    client: AuthenticatedClient | Client,
    after_version: int | Unset = 0,
    limit: int | Unset = 50,
) -> ManagedRealtimeScheduleHistoryResponse | Problem | None:
    """Inspect schedule changes and recorded delivery attempts

     Events commit with schedule changes. Up to 128 entries are retained per
    schedule, ordered by version. history_truncated marks missing earlier
    history, including migrated baseline records. Terminal history expires
    with its schedule receipt. Preview, ownership, MFA, and read scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        after_version (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeScheduleHistoryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        schedule_id=schedule_id,
        client=client,
        after_version=after_version,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    client: AuthenticatedClient | Client,
    after_version: int | Unset = 0,
    limit: int | Unset = 50,
) -> Response[ManagedRealtimeScheduleHistoryResponse | Problem]:
    """Inspect schedule changes and recorded delivery attempts

     Events commit with schedule changes. Up to 128 entries are retained per
    schedule, ordered by version. history_truncated marks missing earlier
    history, including migrated baseline records. Terminal history expires
    with its schedule receipt. Preview, ownership, MFA, and read scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        after_version (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeScheduleHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        schedule_id=schedule_id,
        after_version=after_version,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    client: AuthenticatedClient | Client,
    after_version: int | Unset = 0,
    limit: int | Unset = 50,
) -> ManagedRealtimeScheduleHistoryResponse | Problem | None:
    """Inspect schedule changes and recorded delivery attempts

     Events commit with schedule changes. Up to 128 entries are retained per
    schedule, ordered by version. history_truncated marks missing earlier
    history, including migrated baseline records. Terminal history expires
    with its schedule receipt. Preview, ownership, MFA, and read scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        after_version (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeScheduleHistoryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            schedule_id=schedule_id,
            client=client,
            after_version=after_version,
            limit=limit,
        )
    ).parsed
