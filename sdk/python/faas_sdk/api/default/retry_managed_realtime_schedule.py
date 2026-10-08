from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_schedule_response import ManagedRealtimeScheduleResponse
from ...models.problem import Problem
from ...models.retry_managed_realtime_schedule_body import RetryManagedRealtimeScheduleBody
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    body: RetryManagedRealtimeScheduleBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}/retry".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            schedule_id=quote(str(schedule_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeScheduleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeScheduleResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedRealtimeScheduleResponse | Problem]:
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
    body: RetryManagedRealtimeScheduleBody,
) -> Response[ManagedRealtimeScheduleResponse | Problem]:
    """Retry a failed schedule using its existing ID and payload

     Requires the current version. Resets the cycle attempt budget, preserves lifetime attempts, and
    advances the version. Published or canceled schedules cannot retry.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (RetryManagedRealtimeScheduleBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeScheduleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        schedule_id=schedule_id,
        body=body,
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
    body: RetryManagedRealtimeScheduleBody,
) -> ManagedRealtimeScheduleResponse | Problem | None:
    """Retry a failed schedule using its existing ID and payload

     Requires the current version. Resets the cycle attempt budget, preserves lifetime attempts, and
    advances the version. Published or canceled schedules cannot retry.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (RetryManagedRealtimeScheduleBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeScheduleResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        schedule_id=schedule_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RetryManagedRealtimeScheduleBody,
) -> Response[ManagedRealtimeScheduleResponse | Problem]:
    """Retry a failed schedule using its existing ID and payload

     Requires the current version. Resets the cycle attempt budget, preserves lifetime attempts, and
    advances the version. Published or canceled schedules cannot retry.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (RetryManagedRealtimeScheduleBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeScheduleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        schedule_id=schedule_id,
        body=body,
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
    body: RetryManagedRealtimeScheduleBody,
) -> ManagedRealtimeScheduleResponse | Problem | None:
    """Retry a failed schedule using its existing ID and payload

     Requires the current version. Resets the cycle attempt budget, preserves lifetime attempts, and
    advances the version. Published or canceled schedules cannot retry.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (RetryManagedRealtimeScheduleBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeScheduleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            schedule_id=schedule_id,
            client=client,
            body=body,
        )
    ).parsed
