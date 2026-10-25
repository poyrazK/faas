from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group_settlement import RealtimeConsumerGroupSettlement
from ...models.realtime_consumer_group_settlement_result import RealtimeConsumerGroupSettlementResult
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    body: RealtimeConsumerGroupSettlement,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle".format(
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
) -> Any | RealtimeConsumerGroupSettlementResult | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupSettlementResult.from_dict(response.json())

        return response_200

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
) -> Response[Any | RealtimeConsumerGroupSettlementResult]:
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
    body: RealtimeConsumerGroupSettlement,
) -> Response[Any | RealtimeConsumerGroupSettlementResult]:
    """Complete, retry or renew an unexpired claim

     The sequence and opaque token must identify the current unexpired lease. Completion deletes its
    payload snapshot and increments the group completion count. Retries reaching max_attempts become
    dead letters. Duplicate settlement or a replaced/expired token returns 409; no exactly-once
    execution guarantee.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupSettlementResult]
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
    body: RealtimeConsumerGroupSettlement,
) -> Any | RealtimeConsumerGroupSettlementResult | None:
    """Complete, retry or renew an unexpired claim

     The sequence and opaque token must identify the current unexpired lease. Completion deletes its
    payload snapshot and increments the group completion count. Retries reaching max_attempts become
    dead letters. Duplicate settlement or a replaced/expired token returns 409; no exactly-once
    execution guarantee.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupSettlementResult
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
    body: RealtimeConsumerGroupSettlement,
) -> Response[Any | RealtimeConsumerGroupSettlementResult]:
    """Complete, retry or renew an unexpired claim

     The sequence and opaque token must identify the current unexpired lease. Completion deletes its
    payload snapshot and increments the group completion count. Retries reaching max_attempts become
    dead letters. Duplicate settlement or a replaced/expired token returns 409; no exactly-once
    execution guarantee.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupSettlementResult]
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
    body: RealtimeConsumerGroupSettlement,
) -> Any | RealtimeConsumerGroupSettlementResult | None:
    """Complete, retry or renew an unexpired claim

     The sequence and opaque token must identify the current unexpired lease. Completion deletes its
    payload snapshot and increments the group completion count. Retries reaching max_attempts become
    dead letters. Duplicate settlement or a replaced/expired token returns 409; no exactly-once
    execution guarantee.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        body (RealtimeConsumerGroupSettlement): Realtime Consumer Group Settlement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupSettlementResult
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
