from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dev_bridge_webhook_replay import DevBridgeWebhookReplay
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    replay: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/dev/bridges/{id}/webhook-replays/{replay}".format(
            id=quote(str(id), safe=""),
            replay=quote(str(replay), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DevBridgeWebhookReplay | Problem | None:
    if response.status_code == 200:
        response_200 = DevBridgeWebhookReplay.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DevBridgeWebhookReplay | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    replay: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DevBridgeWebhookReplay | Problem]:
    """Inspect an owned development webhook copy receipt.

    Args:
        id (str):
        replay (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevBridgeWebhookReplay | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        replay=replay,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    replay: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> DevBridgeWebhookReplay | Problem | None:
    """Inspect an owned development webhook copy receipt.

    Args:
        id (str):
        replay (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevBridgeWebhookReplay | Problem
    """

    return sync_detailed(
        id=id,
        replay=replay,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    replay: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DevBridgeWebhookReplay | Problem]:
    """Inspect an owned development webhook copy receipt.

    Args:
        id (str):
        replay (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevBridgeWebhookReplay | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        replay=replay,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    replay: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> DevBridgeWebhookReplay | Problem | None:
    """Inspect an owned development webhook copy receipt.

    Args:
        id (str):
        replay (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevBridgeWebhookReplay | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            replay=replay,
            client=client,
        )
    ).parsed
