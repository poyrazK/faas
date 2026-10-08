from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_schedule_request import ManagedRealtimeScheduleRequest
from ...models.managed_realtime_schedule_response import ManagedRealtimeScheduleResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    schedule_id: str,
    *,
    body: ManagedRealtimeScheduleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/{schedule_id}".format(
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
) -> Any | ManagedRealtimeScheduleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeScheduleResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ManagedRealtimeScheduleResponse | Problem]:
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
    body: ManagedRealtimeScheduleRequest,
) -> Response[Any | ManagedRealtimeScheduleResponse | Problem]:
    """Schedule a retained channel event using an idempotent schedule ID

     Future delivery within 30 days; timestamps normalize to microseconds.
    Up to 256 pending and recent terminal schedules per endpoint. Terminal
    receipts remain available for 24 hours. Identical ID/content retries
    return the existing record; different content conflicts. Schema and reducer
    validation run at delivery time. Preview gate and deploy-write scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (ManagedRealtimeScheduleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeScheduleResponse | Problem]
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
    body: ManagedRealtimeScheduleRequest,
) -> Any | ManagedRealtimeScheduleResponse | Problem | None:
    """Schedule a retained channel event using an idempotent schedule ID

     Future delivery within 30 days; timestamps normalize to microseconds.
    Up to 256 pending and recent terminal schedules per endpoint. Terminal
    receipts remain available for 24 hours. Identical ID/content retries
    return the existing record; different content conflicts. Schema and reducer
    validation run at delivery time. Preview gate and deploy-write scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (ManagedRealtimeScheduleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeScheduleResponse | Problem
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
    body: ManagedRealtimeScheduleRequest,
) -> Response[Any | ManagedRealtimeScheduleResponse | Problem]:
    """Schedule a retained channel event using an idempotent schedule ID

     Future delivery within 30 days; timestamps normalize to microseconds.
    Up to 256 pending and recent terminal schedules per endpoint. Terminal
    receipts remain available for 24 hours. Identical ID/content retries
    return the existing record; different content conflicts. Schema and reducer
    validation run at delivery time. Preview gate and deploy-write scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (ManagedRealtimeScheduleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeScheduleResponse | Problem]
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
    body: ManagedRealtimeScheduleRequest,
) -> Any | ManagedRealtimeScheduleResponse | Problem | None:
    """Schedule a retained channel event using an idempotent schedule ID

     Future delivery within 30 days; timestamps normalize to microseconds.
    Up to 256 pending and recent terminal schedules per endpoint. Terminal
    receipts remain available for 24 hours. Identical ID/content retries
    return the existing record; different content conflicts. Schema and reducer
    validation run at delivery time. Preview gate and deploy-write scopes apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        schedule_id (str):
        body (ManagedRealtimeScheduleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeScheduleResponse | Problem
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
