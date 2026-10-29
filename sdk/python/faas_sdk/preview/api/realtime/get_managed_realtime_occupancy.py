from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_occupancy_response import ManagedRealtimeOccupancyResponse
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    channel: list[str],
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_channel = channel

    params["channel"] = json_channel

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/occupancy".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeOccupancyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeOccupancyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedRealtimeOccupancyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    channel: list[str],
) -> Response[ManagedRealtimeOccupancyResponse | Problem]:
    """Count a managed realtime channel's subscribers and presence members.

     Returns fleet-wide counts for each requested channel.
    `subscribers` counts every open subscription: WebSocket, read-only
    ticket and SSE. `presence_members` counts distinct presence members;
    a principal-scoped member with several connections counts once.
    Counts are a point-in-time snapshot. `partial` is true when one or
    more active realtime nodes could not be asked; the counts then cover
    the nodes that answered.

    Args:
        slug (str):
        id (str):
        channel (list[str]):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeOccupancyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    channel: list[str],
) -> ManagedRealtimeOccupancyResponse | Problem | None:
    """Count a managed realtime channel's subscribers and presence members.

     Returns fleet-wide counts for each requested channel.
    `subscribers` counts every open subscription: WebSocket, read-only
    ticket and SSE. `presence_members` counts distinct presence members;
    a principal-scoped member with several connections counts once.
    Counts are a point-in-time snapshot. `partial` is true when one or
    more active realtime nodes could not be asked; the counts then cover
    the nodes that answered.

    Args:
        slug (str):
        id (str):
        channel (list[str]):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeOccupancyResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        channel=channel,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    channel: list[str],
) -> Response[ManagedRealtimeOccupancyResponse | Problem]:
    """Count a managed realtime channel's subscribers and presence members.

     Returns fleet-wide counts for each requested channel.
    `subscribers` counts every open subscription: WebSocket, read-only
    ticket and SSE. `presence_members` counts distinct presence members;
    a principal-scoped member with several connections counts once.
    Counts are a point-in-time snapshot. `partial` is true when one or
    more active realtime nodes could not be asked; the counts then cover
    the nodes that answered.

    Args:
        slug (str):
        id (str):
        channel (list[str]):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeOccupancyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    channel: list[str],
) -> ManagedRealtimeOccupancyResponse | Problem | None:
    """Count a managed realtime channel's subscribers and presence members.

     Returns fleet-wide counts for each requested channel.
    `subscribers` counts every open subscription: WebSocket, read-only
    ticket and SSE. `presence_members` counts distinct presence members;
    a principal-scoped member with several connections counts once.
    Counts are a point-in-time snapshot. `partial` is true when one or
    more active realtime nodes could not be asked; the counts then cover
    the nodes that answered.

    Args:
        slug (str):
        id (str):
        channel (list[str]):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeOccupancyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            channel=channel,
        )
    ).parsed
