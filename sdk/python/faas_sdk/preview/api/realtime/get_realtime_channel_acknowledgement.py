from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_acknowledgement import RealtimeAcknowledgement
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    *,
    principal: str,
    subscription: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["principal"] = principal

    params["subscription"] = subscription

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/acknowledgement".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeAcknowledgement | None:
    if response.status_code == 200:
        response_200 = RealtimeAcknowledgement.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | RealtimeAcknowledgement]:
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
    principal: str,
    subscription: str,
) -> Response[Any | RealtimeAcknowledgement]:
    """Read the latest actual ACK for a named durable consumer

     Read-only and does not create or refresh a checkpoint. Requires history preview.
    ACKs are consumer assertions; baselines and resets are not processing receipts.
    Only the most recent advancing ACK is retained, not an individual-message ledger.

    Args:
        slug (str):
        id (str):
        channel (str):
        principal (str):
        subscription (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeAcknowledgement]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        principal=principal,
        subscription=subscription,
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
    principal: str,
    subscription: str,
) -> Any | RealtimeAcknowledgement | None:
    """Read the latest actual ACK for a named durable consumer

     Read-only and does not create or refresh a checkpoint. Requires history preview.
    ACKs are consumer assertions; baselines and resets are not processing receipts.
    Only the most recent advancing ACK is retained, not an individual-message ledger.

    Args:
        slug (str):
        id (str):
        channel (str):
        principal (str):
        subscription (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeAcknowledgement
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
        principal=principal,
        subscription=subscription,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
    principal: str,
    subscription: str,
) -> Response[Any | RealtimeAcknowledgement]:
    """Read the latest actual ACK for a named durable consumer

     Read-only and does not create or refresh a checkpoint. Requires history preview.
    ACKs are consumer assertions; baselines and resets are not processing receipts.
    Only the most recent advancing ACK is retained, not an individual-message ledger.

    Args:
        slug (str):
        id (str):
        channel (str):
        principal (str):
        subscription (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeAcknowledgement]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        principal=principal,
        subscription=subscription,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    *,
    client: AuthenticatedClient,
    principal: str,
    subscription: str,
) -> Any | RealtimeAcknowledgement | None:
    """Read the latest actual ACK for a named durable consumer

     Read-only and does not create or refresh a checkpoint. Requires history preview.
    ACKs are consumer assertions; baselines and resets are not processing receipts.
    Only the most recent advancing ACK is retained, not an individual-message ledger.

    Args:
        slug (str):
        id (str):
        channel (str):
        principal (str):
        subscription (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeAcknowledgement
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
            principal=principal,
            subscription=subscription,
        )
    ).parsed
