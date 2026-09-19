from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_binding_status_response import QueueBindingStatusResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/queue-bindings/{id}/status".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueBindingStatusResponse | None:
    if response.status_code == 200:
        response_200 = QueueBindingStatusResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueBindingStatusResponse]:
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
) -> Response[Problem | QueueBindingStatusResponse]:
    """Read queue binding consumer state and queue counters.

     Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`); it does not
    claim to be a broker connection liveness signal.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueBindingStatusResponse]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | QueueBindingStatusResponse | None:
    """Read queue binding consumer state and queue counters.

     Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`); it does not
    claim to be a broker connection liveness signal.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueBindingStatusResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | QueueBindingStatusResponse]:
    """Read queue binding consumer state and queue counters.

     Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`); it does not
    claim to be a broker connection liveness signal.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueBindingStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | QueueBindingStatusResponse | None:
    """Read queue binding consumer state and queue counters.

     Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`); it does not
    claim to be a broker connection liveness signal.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueBindingStatusResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
