from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group_delivery_history import RealtimeConsumerGroupDeliveryHistory
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    sequence: str,
    *,
    after_id: str | Unset = "0",
    limit: int | Unset = 32,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after_id"] = after_id

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/messages/{sequence}/history".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
            sequence=quote(str(sequence), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeConsumerGroupDeliveryHistory | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupDeliveryHistory.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | RealtimeConsumerGroupDeliveryHistory]:
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
    sequence: str,
    *,
    client: AuthenticatedClient,
    after_id: str | Unset = "0",
    limit: int | Unset = 32,
) -> Response[Any | RealtimeConsumerGroupDeliveryHistory]:
    """Read a message's observed consumer-group delivery timeline

     Account ownership, read scope and MFA required. Bounded 24-hour history, at most 1024 events per
    group shared across messages. No pre-migration backfill or renewals. Completions remain after
    snapshot deletion. Empty results do not distinguish unprocessed, completed before migration,
    filtered or pruned messages. group_pruned_events counts expired/capacity-pruned observations across
    the whole group, not solely this message. Reads prune expired history under the group lock. Events
    are ordered by ID; observation timestamps can lag actual lease/deadline expiry. No payload,
    lease/credential token, token hash or worker identity is returned.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        sequence (str):
        after_id (str | Unset):  Default: '0'.
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupDeliveryHistory]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        sequence=sequence,
        after_id=after_id,
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
    group: str,
    sequence: str,
    *,
    client: AuthenticatedClient,
    after_id: str | Unset = "0",
    limit: int | Unset = 32,
) -> Any | RealtimeConsumerGroupDeliveryHistory | None:
    """Read a message's observed consumer-group delivery timeline

     Account ownership, read scope and MFA required. Bounded 24-hour history, at most 1024 events per
    group shared across messages. No pre-migration backfill or renewals. Completions remain after
    snapshot deletion. Empty results do not distinguish unprocessed, completed before migration,
    filtered or pruned messages. group_pruned_events counts expired/capacity-pruned observations across
    the whole group, not solely this message. Reads prune expired history under the group lock. Events
    are ordered by ID; observation timestamps can lag actual lease/deadline expiry. No payload,
    lease/credential token, token hash or worker identity is returned.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        sequence (str):
        after_id (str | Unset):  Default: '0'.
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupDeliveryHistory
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        sequence=sequence,
        client=client,
        after_id=after_id,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    sequence: str,
    *,
    client: AuthenticatedClient,
    after_id: str | Unset = "0",
    limit: int | Unset = 32,
) -> Response[Any | RealtimeConsumerGroupDeliveryHistory]:
    """Read a message's observed consumer-group delivery timeline

     Account ownership, read scope and MFA required. Bounded 24-hour history, at most 1024 events per
    group shared across messages. No pre-migration backfill or renewals. Completions remain after
    snapshot deletion. Empty results do not distinguish unprocessed, completed before migration,
    filtered or pruned messages. group_pruned_events counts expired/capacity-pruned observations across
    the whole group, not solely this message. Reads prune expired history under the group lock. Events
    are ordered by ID; observation timestamps can lag actual lease/deadline expiry. No payload,
    lease/credential token, token hash or worker identity is returned.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        sequence (str):
        after_id (str | Unset):  Default: '0'.
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupDeliveryHistory]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        sequence=sequence,
        after_id=after_id,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    sequence: str,
    *,
    client: AuthenticatedClient,
    after_id: str | Unset = "0",
    limit: int | Unset = 32,
) -> Any | RealtimeConsumerGroupDeliveryHistory | None:
    """Read a message's observed consumer-group delivery timeline

     Account ownership, read scope and MFA required. Bounded 24-hour history, at most 1024 events per
    group shared across messages. No pre-migration backfill or renewals. Completions remain after
    snapshot deletion. Empty results do not distinguish unprocessed, completed before migration,
    filtered or pruned messages. group_pruned_events counts expired/capacity-pruned observations across
    the whole group, not solely this message. Reads prune expired history under the group lock. Events
    are ordered by ID; observation timestamps can lag actual lease/deadline expiry. No payload,
    lease/credential token, token hash or worker identity is returned.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        sequence (str):
        after_id (str | Unset):  Default: '0'.
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupDeliveryHistory
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            sequence=sequence,
            client=client,
            after_id=after_id,
            limit=limit,
        )
    ).parsed
