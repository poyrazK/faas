from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.control_realtime_replay_job_action import (
    ControlRealtimeReplayJobAction,
)
from ...models.realtime_replay_job import RealtimeReplayJob
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    job: UUID,
    action: ControlRealtimeReplayJobAction,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/{job}/{action}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
            job=quote(str(job), safe=""),
            action=quote(str(action), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeReplayJob | None:
    if response.status_code == 200:
        response_200 = RealtimeReplayJob.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | RealtimeReplayJob]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    job: UUID,
    action: ControlRealtimeReplayJobAction,
    *,
    client: AuthenticatedClient,
) -> Response[Any | RealtimeReplayJob]:
    """Pause, resume, cancel or start a reviewed replay

     Account deploy-write scope, ownership and MFA required. Pause/resume/cancel apply to active real or
    dry-run jobs. Start requires a completed dry run with eligible failures, and creates a real replay
    at the report rate from only those exact reviewed identities. Rechecks identity, deadlines and
    ordering before each real replay. Repeated start returns the same job while retained; if that job
    was pruned it returns 409 and never starts another. If the report itself was pruned it returns 404.
    Promotion and the report link commit together. Both modes share the four-active-job quota. Terminal
    jobs reject pause/resume/cancel. Cancellation preserves committed replays.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        job (UUID):
        action (ControlRealtimeReplayJobAction):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeReplayJob]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        job=job,
        action=action,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    job: UUID,
    action: ControlRealtimeReplayJobAction,
    *,
    client: AuthenticatedClient,
) -> Any | RealtimeReplayJob | None:
    """Pause, resume, cancel or start a reviewed replay

     Account deploy-write scope, ownership and MFA required. Pause/resume/cancel apply to active real or
    dry-run jobs. Start requires a completed dry run with eligible failures, and creates a real replay
    at the report rate from only those exact reviewed identities. Rechecks identity, deadlines and
    ordering before each real replay. Repeated start returns the same job while retained; if that job
    was pruned it returns 409 and never starts another. If the report itself was pruned it returns 404.
    Promotion and the report link commit together. Both modes share the four-active-job quota. Terminal
    jobs reject pause/resume/cancel. Cancellation preserves committed replays.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        job (UUID):
        action (ControlRealtimeReplayJobAction):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeReplayJob
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        job=job,
        action=action,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    job: UUID,
    action: ControlRealtimeReplayJobAction,
    *,
    client: AuthenticatedClient,
) -> Response[Any | RealtimeReplayJob]:
    """Pause, resume, cancel or start a reviewed replay

     Account deploy-write scope, ownership and MFA required. Pause/resume/cancel apply to active real or
    dry-run jobs. Start requires a completed dry run with eligible failures, and creates a real replay
    at the report rate from only those exact reviewed identities. Rechecks identity, deadlines and
    ordering before each real replay. Repeated start returns the same job while retained; if that job
    was pruned it returns 409 and never starts another. If the report itself was pruned it returns 404.
    Promotion and the report link commit together. Both modes share the four-active-job quota. Terminal
    jobs reject pause/resume/cancel. Cancellation preserves committed replays.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        job (UUID):
        action (ControlRealtimeReplayJobAction):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeReplayJob]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        job=job,
        action=action,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    job: UUID,
    action: ControlRealtimeReplayJobAction,
    *,
    client: AuthenticatedClient,
) -> Any | RealtimeReplayJob | None:
    """Pause, resume, cancel or start a reviewed replay

     Account deploy-write scope, ownership and MFA required. Pause/resume/cancel apply to active real or
    dry-run jobs. Start requires a completed dry run with eligible failures, and creates a real replay
    at the report rate from only those exact reviewed identities. Rechecks identity, deadlines and
    ordering before each real replay. Repeated start returns the same job while retained; if that job
    was pruned it returns 409 and never starts another. If the report itself was pruned it returns 404.
    Promotion and the report link commit together. Both modes share the four-active-job quota. Terminal
    jobs reject pause/resume/cancel. Cancellation preserves committed replays.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        job (UUID):
        action (ControlRealtimeReplayJobAction):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeReplayJob
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            job=job,
            action=action,
            client=client,
        )
    ).parsed
