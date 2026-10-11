from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_consumer_execution_health import EventConsumerExecutionHealth
from ...models.get_event_consumer_execution_health_window import (
    GetEventConsumerExecutionHealthWindow,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    window: GetEventConsumerExecutionHealthWindow | Unset = "5m",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-health".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventConsumerExecutionHealth | Problem | None:
    if response.status_code == 200:
        response_200 = EventConsumerExecutionHealth.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventConsumerExecutionHealth | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    window: GetEventConsumerExecutionHealthWindow | Unset = "5m",
) -> Response[EventConsumerExecutionHealth | Problem]:
    """Inspect consumer execution health over a retained history window.

     Requires `apps:read` or `admin`. Reports current retained execution states and windowed attempt
    outcomes for admitted deliveries and handler replays. Receipt and invocation retention bound
    coverage; history_complete is always false. Counts are not unique event totals. Completion latency
    starts at original event acceptance.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerExecutionHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventConsumerExecutionHealth | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        window=window,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    window: GetEventConsumerExecutionHealthWindow | Unset = "5m",
) -> EventConsumerExecutionHealth | Problem | None:
    """Inspect consumer execution health over a retained history window.

     Requires `apps:read` or `admin`. Reports current retained execution states and windowed attempt
    outcomes for admitted deliveries and handler replays. Receipt and invocation retention bound
    coverage; history_complete is always false. Counts are not unique event totals. Completion latency
    starts at original event acceptance.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerExecutionHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventConsumerExecutionHealth | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        window=window,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    window: GetEventConsumerExecutionHealthWindow | Unset = "5m",
) -> Response[EventConsumerExecutionHealth | Problem]:
    """Inspect consumer execution health over a retained history window.

     Requires `apps:read` or `admin`. Reports current retained execution states and windowed attempt
    outcomes for admitted deliveries and handler replays. Receipt and invocation retention bound
    coverage; history_complete is always false. Counts are not unique event totals. Completion latency
    starts at original event acceptance.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerExecutionHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventConsumerExecutionHealth | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        window=window,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    window: GetEventConsumerExecutionHealthWindow | Unset = "5m",
) -> EventConsumerExecutionHealth | Problem | None:
    """Inspect consumer execution health over a retained history window.

     Requires `apps:read` or `admin`. Reports current retained execution states and windowed attempt
    outcomes for admitted deliveries and handler replays. Receipt and invocation retention bound
    coverage; history_complete is always false. Counts are not unique event totals. Completion latency
    starts at original event acceptance.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerExecutionHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventConsumerExecutionHealth | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            window=window,
        )
    ).parsed
