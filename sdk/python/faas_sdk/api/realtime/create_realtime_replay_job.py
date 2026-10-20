from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_replay_job import RealtimeReplayJob
from ...models.realtime_replay_job_request import RealtimeReplayJobRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeReplayJobRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeReplayJob | None:
    if response.status_code == 201:
        response_201 = RealtimeReplayJob.from_dict(response.json())

        return response_201

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
    *,
    client: AuthenticatedClient,
    body: RealtimeReplayJobRequest,
) -> Response[Any | RealtimeReplayJob]:
    """Create a background dead-letter replay job

     Create a background dead-letter replay job. Account ownership and MFA required. Read scope for
    listing; deploy-write scope for creation. Creation freezes up to 128 current failure identities
    matching the filters. Rate is an upper limit with no catch-up burst. Four running or paused jobs per
    group; recent terminal jobs retained in bounded history. Disabled endpoints and inactive owners
    suspend processing. Skips are terminal per item. Database failures roll back progress and retry on
    subsequent passes. Creation is not idempotent: inspect the job list after an uncertain response
    before retrying.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeReplayJobRequest): Realtime Replay Job Request.

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
        body=body,
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
    *,
    client: AuthenticatedClient,
    body: RealtimeReplayJobRequest,
) -> Any | RealtimeReplayJob | None:
    """Create a background dead-letter replay job

     Create a background dead-letter replay job. Account ownership and MFA required. Read scope for
    listing; deploy-write scope for creation. Creation freezes up to 128 current failure identities
    matching the filters. Rate is an upper limit with no catch-up burst. Four running or paused jobs per
    group; recent terminal jobs retained in bounded history. Disabled endpoints and inactive owners
    suspend processing. Skips are terminal per item. Database failures roll back progress and retry on
    subsequent passes. Creation is not idempotent: inspect the job list after an uncertain response
    before retrying.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeReplayJobRequest): Realtime Replay Job Request.

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
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeReplayJobRequest,
) -> Response[Any | RealtimeReplayJob]:
    """Create a background dead-letter replay job

     Create a background dead-letter replay job. Account ownership and MFA required. Read scope for
    listing; deploy-write scope for creation. Creation freezes up to 128 current failure identities
    matching the filters. Rate is an upper limit with no catch-up burst. Four running or paused jobs per
    group; recent terminal jobs retained in bounded history. Disabled endpoints and inactive owners
    suspend processing. Skips are terminal per item. Database failures roll back progress and retry on
    subsequent passes. Creation is not idempotent: inspect the job list after an uncertain response
    before retrying.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeReplayJobRequest): Realtime Replay Job Request.

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
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    body: RealtimeReplayJobRequest,
) -> Any | RealtimeReplayJob | None:
    """Create a background dead-letter replay job

     Create a background dead-letter replay job. Account ownership and MFA required. Read scope for
    listing; deploy-write scope for creation. Creation freezes up to 128 current failure identities
    matching the filters. Rate is an upper limit with no catch-up burst. Four running or paused jobs per
    group; recent terminal jobs retained in bounded history. Disabled endpoints and inactive owners
    suspend processing. Skips are terminal per item. Database failures roll back progress and retry on
    subsequent passes. Creation is not idempotent: inspect the job list after an uncertain response
    before retrying.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeReplayJobRequest): Realtime Replay Job Request.

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
            client=client,
            body=body,
        )
    ).parsed
