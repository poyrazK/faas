from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group import RealtimeConsumerGroup
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | list[RealtimeConsumerGroup] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = RealtimeConsumerGroup.from_dict(response_200_item_data)

            response_200.append(response_200_item)

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
) -> Response[Any | list[RealtimeConsumerGroup]]:
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
    client: AuthenticatedClient,
) -> Response[Any | list[RealtimeConsumerGroup]]:
    """List consumer group lag and worker status

     Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write
    operations require write scope. Delivery is at least once.

    Args:
        slug (str):
        id (str):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | list[RealtimeConsumerGroup]]
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
    channel: str,
    *,
    client: AuthenticatedClient,
) -> Any | list[RealtimeConsumerGroup] | None:
    """List consumer group lag and worker status

     Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write
    operations require write scope. Delivery is at least once.

    Args:
        slug (str):
        id (str):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | list[RealtimeConsumerGroup]
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | list[RealtimeConsumerGroup]]:
    """List consumer group lag and worker status

     Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write
    operations require write scope. Delivery is at least once.

    Args:
        slug (str):
        id (str):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | list[RealtimeConsumerGroup]]
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
    channel: str,
    *,
    client: AuthenticatedClient,
) -> Any | list[RealtimeConsumerGroup] | None:
    """List consumer group lag and worker status

     Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write
    operations require write scope. Delivery is at least once.

    Args:
        slug (str):
        id (str):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | list[RealtimeConsumerGroup]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
        )
    ).parsed
