from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    connection_id: UUID,
    channel: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            connection_id=quote(str(connection_id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    connection_id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Subscribe one live connection to a channel.

    Args:
        slug (str):
        id (str):
        connection_id (UUID):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        connection_id=connection_id,
        channel=channel,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    connection_id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Subscribe one live connection to a channel.

    Args:
        slug (str):
        id (str):
        connection_id (UUID):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        connection_id=connection_id,
        channel=channel,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    connection_id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Subscribe one live connection to a channel.

    Args:
        slug (str):
        id (str):
        connection_id (UUID):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        connection_id=connection_id,
        channel=channel,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    connection_id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Subscribe one live connection to a channel.

    Args:
        slug (str):
        id (str):
        connection_id (UUID):
        channel (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            connection_id=connection_id,
            channel=channel,
            client=client,
        )
    ).parsed
