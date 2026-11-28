from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_retention_policy import RealtimeConsumerGroupRetentionPolicy
from ...models.realtime_consumer_group_retention_request import RealtimeConsumerGroupRetentionRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeConsumerGroupRetentionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention".format(
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
) -> Any | Problem | RealtimeConsumerGroupRetentionPolicy | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupRetentionPolicy.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
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
) -> Response[Any | Problem | RealtimeConsumerGroupRetentionPolicy]:
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
    body: RealtimeConsumerGroupRetentionRequest,
) -> Response[Any | Problem | RealtimeConsumerGroupRetentionPolicy]:
    """Configure dead-letter retention

     Configure dead-letter retention. Requires app ownership and MFA; writes require admin scope. Zero
    keeps dead letters indefinitely. Otherwise choose 3600 to 2592000 seconds. Ordered groups require
    skip policy. Cleanup follows a persisted warning decision by at least 300 seconds; webhook delivery
    does not gate cleanup. Replay protects the snapshot when it commits before cleanup. Changed policies
    rearm warnings.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupRetentionRequest): Realtime Consumer Group Retention Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupRetentionPolicy]
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
    body: RealtimeConsumerGroupRetentionRequest,
) -> Any | Problem | RealtimeConsumerGroupRetentionPolicy | None:
    """Configure dead-letter retention

     Configure dead-letter retention. Requires app ownership and MFA; writes require admin scope. Zero
    keeps dead letters indefinitely. Otherwise choose 3600 to 2592000 seconds. Ordered groups require
    skip policy. Cleanup follows a persisted warning decision by at least 300 seconds; webhook delivery
    does not gate cleanup. Replay protects the snapshot when it commits before cleanup. Changed policies
    rearm warnings.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupRetentionRequest): Realtime Consumer Group Retention Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupRetentionPolicy
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
    body: RealtimeConsumerGroupRetentionRequest,
) -> Response[Any | Problem | RealtimeConsumerGroupRetentionPolicy]:
    """Configure dead-letter retention

     Configure dead-letter retention. Requires app ownership and MFA; writes require admin scope. Zero
    keeps dead letters indefinitely. Otherwise choose 3600 to 2592000 seconds. Ordered groups require
    skip policy. Cleanup follows a persisted warning decision by at least 300 seconds; webhook delivery
    does not gate cleanup. Replay protects the snapshot when it commits before cleanup. Changed policies
    rearm warnings.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupRetentionRequest): Realtime Consumer Group Retention Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupRetentionPolicy]
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
    body: RealtimeConsumerGroupRetentionRequest,
) -> Any | Problem | RealtimeConsumerGroupRetentionPolicy | None:
    """Configure dead-letter retention

     Configure dead-letter retention. Requires app ownership and MFA; writes require admin scope. Zero
    keeps dead letters indefinitely. Otherwise choose 3600 to 2592000 seconds. Ordered groups require
    skip policy. Cleanup follows a persisted warning decision by at least 300 seconds; webhook delivery
    does not gate cleanup. Replay protects the snapshot when it commits before cleanup. Changed policies
    rearm warnings.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupRetentionRequest): Realtime Consumer Group Retention Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupRetentionPolicy
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
