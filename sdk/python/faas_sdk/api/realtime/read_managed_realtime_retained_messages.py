from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_retained_history_response import ManagedRealtimeRetainedHistoryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    *,
    after: int,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeRetainedHistoryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeRetainedHistoryResponse.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[ManagedRealtimeRetainedHistoryResponse | Problem]:
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
    *,
    client: AuthenticatedClient | Client,
    after: int,
    limit: int | Unset = 100,
) -> Response[ManagedRealtimeRetainedHistoryResponse | Problem]:
    """Read a page of retained channel history after a sequence.

     A cursor older than retained history returns 410 with code history_unavailable. This management API
    is not a WebSocket resume protocol.

    Args:
        slug (str):
        id (str):
        channel (str):
        after (int):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeRetainedHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    after: int,
    limit: int | Unset = 100,
) -> ManagedRealtimeRetainedHistoryResponse | Problem | None:
    """Read a page of retained channel history after a sequence.

     A cursor older than retained history returns 410 with code history_unavailable. This management API
    is not a WebSocket resume protocol.

    Args:
        slug (str):
        id (str):
        channel (str):
        after (int):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeRetainedHistoryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    after: int,
    limit: int | Unset = 100,
) -> Response[ManagedRealtimeRetainedHistoryResponse | Problem]:
    """Read a page of retained channel history after a sequence.

     A cursor older than retained history returns 410 with code history_unavailable. This management API
    is not a WebSocket resume protocol.

    Args:
        slug (str):
        id (str):
        channel (str):
        after (int):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeRetainedHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    after: int,
    limit: int | Unset = 100,
) -> ManagedRealtimeRetainedHistoryResponse | Problem | None:
    """Read a page of retained channel history after a sequence.

     A cursor older than retained history returns 410 with code history_unavailable. This management API
    is not a WebSocket resume protocol.

    Args:
        slug (str):
        id (str):
        channel (str):
        after (int):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeRetainedHistoryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
            after=after,
            limit=limit,
        )
    ).parsed
