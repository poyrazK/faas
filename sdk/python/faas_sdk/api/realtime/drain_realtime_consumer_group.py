from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group_drain_state import RealtimeConsumerGroupDrainState
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/drain".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeConsumerGroupDrainState | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupDrainState.from_dict(response.json())

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
) -> Response[Any | RealtimeConsumerGroupDrainState]:
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
) -> Response[Any | RealtimeConsumerGroupDrainState]:
    """Drain active consumer-group leases

     Idempotently stop new claims and enter persistent drain mode. Requires account admin scope and MFA,
    or the explicit drain worker permission. Existing leases may renew or settle. Drained means no
    unexpired leases remain; it does not prove handler side effects have stopped. Retry/dead/unassigned
    work is preserved. Resume explicitly after deployment; pause switches to ordinary paused mode.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupDrainState]
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
) -> Any | RealtimeConsumerGroupDrainState | None:
    """Drain active consumer-group leases

     Idempotently stop new claims and enter persistent drain mode. Requires account admin scope and MFA,
    or the explicit drain worker permission. Existing leases may renew or settle. Drained means no
    unexpired leases remain; it does not prove handler side effects have stopped. Retry/dead/unassigned
    work is preserved. Resume explicitly after deployment; pause switches to ordinary paused mode.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupDrainState
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
) -> Response[Any | RealtimeConsumerGroupDrainState]:
    """Drain active consumer-group leases

     Idempotently stop new claims and enter persistent drain mode. Requires account admin scope and MFA,
    or the explicit drain worker permission. Existing leases may renew or settle. Drained means no
    unexpired leases remain; it does not prove handler side effects have stopped. Retry/dead/unassigned
    work is preserved. Resume explicitly after deployment; pause switches to ordinary paused mode.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupDrainState]
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
) -> Any | RealtimeConsumerGroupDrainState | None:
    """Drain active consumer-group leases

     Idempotently stop new claims and enter persistent drain mode. Requires account admin scope and MFA,
    or the explicit drain worker permission. Existing leases may renew or settle. Drained means no
    unexpired leases remain; it does not prove handler side effects have stopped. Retry/dead/unassigned
    work is preserved. Resume explicitly after deployment; pause switches to ordinary paused mode.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupDrainState
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
