from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_drain_response import ManagedRealtimeDrainResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    drain_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/connections/drain/{drain_id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            drain_id=quote(str(drain_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeDrainResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeDrainResponse.from_dict(response.json())

        return response_200

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
) -> Response[ManagedRealtimeDrainResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    drain_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimeDrainResponse | Problem]:
    """Get the durable result of a realtime connection drain.

     Returns a previously recorded drain result scoped to the application and endpoint.

    Args:
        slug (str):
        id (str):
        drain_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeDrainResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        drain_id=drain_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    drain_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimeDrainResponse | Problem | None:
    """Get the durable result of a realtime connection drain.

     Returns a previously recorded drain result scoped to the application and endpoint.

    Args:
        slug (str):
        id (str):
        drain_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeDrainResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        drain_id=drain_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    drain_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimeDrainResponse | Problem]:
    """Get the durable result of a realtime connection drain.

     Returns a previously recorded drain result scoped to the application and endpoint.

    Args:
        slug (str):
        id (str):
        drain_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeDrainResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        drain_id=drain_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    drain_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimeDrainResponse | Problem | None:
    """Get the durable result of a realtime connection drain.

     Returns a previously recorded drain result scoped to the application and endpoint.

    Args:
        slug (str):
        id (str):
        drain_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeDrainResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            drain_id=drain_id,
            client=client,
        )
    ).parsed
