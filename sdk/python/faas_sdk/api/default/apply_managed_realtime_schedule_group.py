from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_managed_realtime_schedule_group_group_action import (
    ApplyManagedRealtimeScheduleGroupGroupAction,
)
from ...models.apply_managed_realtime_schedule_group_response_200 import ApplyManagedRealtimeScheduleGroupResponse200
from ...models.managed_realtime_schedule_group_request import ManagedRealtimeScheduleGroupRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    group: str,
    group_action: ApplyManagedRealtimeScheduleGroupGroupAction,
    *,
    body: ManagedRealtimeScheduleGroupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/groups/{group}/{group_action}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
            group_action=quote(str(group_action), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyManagedRealtimeScheduleGroupResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyManagedRealtimeScheduleGroupResponse200.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplyManagedRealtimeScheduleGroupResponse200 | Problem]:
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
    group: str,
    group_action: ApplyManagedRealtimeScheduleGroupGroupAction,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeScheduleGroupRequest,
) -> Response[ApplyManagedRealtimeScheduleGroupResponse200 | Problem]:
    """Atomically pause, resume, or cancel a channel schedule group

     Requires exactly every pending/paused member's version. Membership/version changes or incompatible
    transitions reject the entire action. Terminal members are excluded. Already-target-state members
    remain unchanged. Supports one-time and recurring schedules; deploy-write scopes, MFA and preview
    gating apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str):
        group_action (ApplyManagedRealtimeScheduleGroupGroupAction):
        body (ManagedRealtimeScheduleGroupRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyManagedRealtimeScheduleGroupResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        group_action=group_action,
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
    group: str,
    group_action: ApplyManagedRealtimeScheduleGroupGroupAction,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeScheduleGroupRequest,
) -> ApplyManagedRealtimeScheduleGroupResponse200 | Problem | None:
    """Atomically pause, resume, or cancel a channel schedule group

     Requires exactly every pending/paused member's version. Membership/version changes or incompatible
    transitions reject the entire action. Terminal members are excluded. Already-target-state members
    remain unchanged. Supports one-time and recurring schedules; deploy-write scopes, MFA and preview
    gating apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str):
        group_action (ApplyManagedRealtimeScheduleGroupGroupAction):
        body (ManagedRealtimeScheduleGroupRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyManagedRealtimeScheduleGroupResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        group_action=group_action,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    group: str,
    group_action: ApplyManagedRealtimeScheduleGroupGroupAction,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeScheduleGroupRequest,
) -> Response[ApplyManagedRealtimeScheduleGroupResponse200 | Problem]:
    """Atomically pause, resume, or cancel a channel schedule group

     Requires exactly every pending/paused member's version. Membership/version changes or incompatible
    transitions reject the entire action. Terminal members are excluded. Already-target-state members
    remain unchanged. Supports one-time and recurring schedules; deploy-write scopes, MFA and preview
    gating apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str):
        group_action (ApplyManagedRealtimeScheduleGroupGroupAction):
        body (ManagedRealtimeScheduleGroupRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyManagedRealtimeScheduleGroupResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        group_action=group_action,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    channel: str,
    group: str,
    group_action: ApplyManagedRealtimeScheduleGroupGroupAction,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeScheduleGroupRequest,
) -> ApplyManagedRealtimeScheduleGroupResponse200 | Problem | None:
    """Atomically pause, resume, or cancel a channel schedule group

     Requires exactly every pending/paused member's version. Membership/version changes or incompatible
    transitions reject the entire action. Terminal members are excluded. Already-target-state members
    remain unchanged. Supports one-time and recurring schedules; deploy-write scopes, MFA and preview
    gating apply.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        group (str):
        group_action (ApplyManagedRealtimeScheduleGroupGroupAction):
        body (ManagedRealtimeScheduleGroupRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyManagedRealtimeScheduleGroupResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            group_action=group_action,
            client=client,
            body=body,
        )
    ).parsed
