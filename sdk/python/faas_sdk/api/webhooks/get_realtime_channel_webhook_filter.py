from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_channel_webhook_filter import RealtimeChannelWebhookFilter
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/webhooks/{id}/realtime-channel".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> RealtimeChannelWebhookFilter | None:
    if response.status_code == 200:
        response_200 = RealtimeChannelWebhookFilter.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[RealtimeChannelWebhookFilter]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[RealtimeChannelWebhookFilter]:
    """Get an exact realtime channel webhook binding

     Requires app ownership and MFA. Explicit realtime.channel.published subscription required for
    writes. Metadata filters match all supplied key/value pairs; no backfill occurs.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[RealtimeChannelWebhookFilter]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
) -> RealtimeChannelWebhookFilter | None:
    """Get an exact realtime channel webhook binding

     Requires app ownership and MFA. Explicit realtime.channel.published subscription required for
    writes. Metadata filters match all supplied key/value pairs; no backfill occurs.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        RealtimeChannelWebhookFilter
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[RealtimeChannelWebhookFilter]:
    """Get an exact realtime channel webhook binding

     Requires app ownership and MFA. Explicit realtime.channel.published subscription required for
    writes. Metadata filters match all supplied key/value pairs; no backfill occurs.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[RealtimeChannelWebhookFilter]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
) -> RealtimeChannelWebhookFilter | None:
    """Get an exact realtime channel webhook binding

     Requires app ownership and MFA. Explicit realtime.channel.published subscription required for
    writes. Metadata filters match all supplied key/value pairs; no backfill occurs.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        RealtimeChannelWebhookFilter
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
