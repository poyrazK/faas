from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_retry_status import RealtimeConsumerGroupRetryStatus
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | RealtimeConsumerGroupRetryStatus | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupRetryStatus.from_dict(response.json())

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
) -> Response[Any | Problem | RealtimeConsumerGroupRetryStatus]:
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
) -> Response[Any | Problem | RealtimeConsumerGroupRetryStatus]:
    """Read consumer-group retry policy

     Requires app ownership and MFA; writes require admin scope. Policies are disabled by default.
    Enabled policies apply capped exponential backoff to reported failures with omitted
    retry_after_seconds. Explicit delays, including zero, override backoff. Exact permanent failure
    codes dead-letter immediately even with an explicit delay. Jitter samples uniformly from half the
    capped delay through the capped delay, in integer seconds. Attempt budgets still apply. Changes
    affect future settlements, not already scheduled retries or expired leases. next_retry_at is the
    earliest delayed ready snapshot; ordering, pause and capacity can delay its claim further.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupRetryStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
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
) -> Any | Problem | RealtimeConsumerGroupRetryStatus | None:
    """Read consumer-group retry policy

     Requires app ownership and MFA; writes require admin scope. Policies are disabled by default.
    Enabled policies apply capped exponential backoff to reported failures with omitted
    retry_after_seconds. Explicit delays, including zero, override backoff. Exact permanent failure
    codes dead-letter immediately even with an explicit delay. Jitter samples uniformly from half the
    capped delay through the capped delay, in integer seconds. Attempt budgets still apply. Changes
    affect future settlements, not already scheduled retries or expired leases. next_retry_at is the
    earliest delayed ready snapshot; ordering, pause and capacity can delay its claim further.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupRetryStatus
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem | RealtimeConsumerGroupRetryStatus]:
    """Read consumer-group retry policy

     Requires app ownership and MFA; writes require admin scope. Policies are disabled by default.
    Enabled policies apply capped exponential backoff to reported failures with omitted
    retry_after_seconds. Explicit delays, including zero, override backoff. Exact permanent failure
    codes dead-letter immediately even with an explicit delay. Jitter samples uniformly from half the
    capped delay through the capped delay, in integer seconds. Attempt budgets still apply. Changes
    affect future settlements, not already scheduled retries or expired leases. next_retry_at is the
    earliest delayed ready snapshot; ordering, pause and capacity can delay its claim further.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupRetryStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
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
) -> Any | Problem | RealtimeConsumerGroupRetryStatus | None:
    """Read consumer-group retry policy

     Requires app ownership and MFA; writes require admin scope. Policies are disabled by default.
    Enabled policies apply capped exponential backoff to reported failures with omitted
    retry_after_seconds. Explicit delays, including zero, override backoff. Exact permanent failure
    codes dead-letter immediately even with an explicit delay. Jitter samples uniformly from half the
    capped delay through the capped delay, in integer seconds. Attempt budgets still apply. Changes
    affect future settlements, not already scheduled retries or expired leases. next_retry_at is the
    earliest delayed ready snapshot; ordering, pause and capacity can delay its claim further.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupRetryStatus
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            client=client,
        )
    ).parsed
