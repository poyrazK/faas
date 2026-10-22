from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_consumer_delivery_health import EventConsumerDeliveryHealth
from ...models.get_event_consumer_delivery_health_window import (
    GetEventConsumerDeliveryHealthWindow,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    window: GetEventConsumerDeliveryHealthWindow | Unset = "5m",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-health".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventConsumerDeliveryHealth | Problem | None:
    if response.status_code == 200:
        response_200 = EventConsumerDeliveryHealth.from_dict(response.json())

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
) -> Response[EventConsumerDeliveryHealth | Problem]:
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
    window: GetEventConsumerDeliveryHealthWindow | Unset = "5m",
) -> Response[EventConsumerDeliveryHealth | Problem]:
    """Inspect subscription delivery health across routing and retained execution evidence.

     Requires `apps:read` or `admin`. Combines independent reads under one bounded deadline, not a single
    database snapshot. Routing counts cover the live backlog. Current execution counts include bounded
    retained handler replays. Attempt rates and completion latency use durable per-subscription window
    evidence, with explicit completeness and retention boundaries. Pending sample blocker counts cover
    at most 50 oldest pending application recipients and are sampled when truncated. Capacity-wait and
    waiting-reason counts cover all retained pending application recipients independently of that
    sample. Component observed_at timestamps describe each read; top-level observed_at marks assembly
    completion. Configured rate and pause/circuit state are controls, not proof that every recipient is
    blocked. Latest successful completion is the latest in bounded retained execution coverage, not an
    all-time watermark.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerDeliveryHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventConsumerDeliveryHealth | Problem]
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
    window: GetEventConsumerDeliveryHealthWindow | Unset = "5m",
) -> EventConsumerDeliveryHealth | Problem | None:
    """Inspect subscription delivery health across routing and retained execution evidence.

     Requires `apps:read` or `admin`. Combines independent reads under one bounded deadline, not a single
    database snapshot. Routing counts cover the live backlog. Current execution counts include bounded
    retained handler replays. Attempt rates and completion latency use durable per-subscription window
    evidence, with explicit completeness and retention boundaries. Pending sample blocker counts cover
    at most 50 oldest pending application recipients and are sampled when truncated. Capacity-wait and
    waiting-reason counts cover all retained pending application recipients independently of that
    sample. Component observed_at timestamps describe each read; top-level observed_at marks assembly
    completion. Configured rate and pause/circuit state are controls, not proof that every recipient is
    blocked. Latest successful completion is the latest in bounded retained execution coverage, not an
    all-time watermark.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerDeliveryHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventConsumerDeliveryHealth | Problem
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
    window: GetEventConsumerDeliveryHealthWindow | Unset = "5m",
) -> Response[EventConsumerDeliveryHealth | Problem]:
    """Inspect subscription delivery health across routing and retained execution evidence.

     Requires `apps:read` or `admin`. Combines independent reads under one bounded deadline, not a single
    database snapshot. Routing counts cover the live backlog. Current execution counts include bounded
    retained handler replays. Attempt rates and completion latency use durable per-subscription window
    evidence, with explicit completeness and retention boundaries. Pending sample blocker counts cover
    at most 50 oldest pending application recipients and are sampled when truncated. Capacity-wait and
    waiting-reason counts cover all retained pending application recipients independently of that
    sample. Component observed_at timestamps describe each read; top-level observed_at marks assembly
    completion. Configured rate and pause/circuit state are controls, not proof that every recipient is
    blocked. Latest successful completion is the latest in bounded retained execution coverage, not an
    all-time watermark.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerDeliveryHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventConsumerDeliveryHealth | Problem]
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
    window: GetEventConsumerDeliveryHealthWindow | Unset = "5m",
) -> EventConsumerDeliveryHealth | Problem | None:
    """Inspect subscription delivery health across routing and retained execution evidence.

     Requires `apps:read` or `admin`. Combines independent reads under one bounded deadline, not a single
    database snapshot. Routing counts cover the live backlog. Current execution counts include bounded
    retained handler replays. Attempt rates and completion latency use durable per-subscription window
    evidence, with explicit completeness and retention boundaries. Pending sample blocker counts cover
    at most 50 oldest pending application recipients and are sampled when truncated. Capacity-wait and
    waiting-reason counts cover all retained pending application recipients independently of that
    sample. Component observed_at timestamps describe each read; top-level observed_at marks assembly
    completion. Configured rate and pause/circuit state are controls, not proof that every recipient is
    blocked. Latest successful completion is the latest in bounded retained execution coverage, not an
    all-time watermark.

    Args:
        slug (str):
        subscription_id (UUID):
        window (GetEventConsumerDeliveryHealthWindow | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventConsumerDeliveryHealth | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            window=window,
        )
    ).parsed
