from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_rate_command_receipt import EventSubscriptionRateCommandReceipt
from ...models.event_subscription_rate_request import EventSubscriptionRateRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    body: EventSubscriptionRateRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/delivery-control/rate-limit".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionRateCommandReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionRateCommandReceipt.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventSubscriptionRateCommandReceipt | Problem]:
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
    body: EventSubscriptionRateRequest,
) -> Response[EventSubscriptionRateCommandReceipt | Problem]:
    """Configure rate bucket.

     Configure rate bucket: Durable per-subscription pacing shared by admission workers. Mutations
    require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a
    recorded rejection with no configuration change. Repeating an unexpired command UUID with identical
    inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second
    drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionRateRequest): Explicit guarded command for per-subscription token-
            bucket pacing. Disabled retains configuration without enforcing the bucket.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionRateCommandReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        body=body,
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
    body: EventSubscriptionRateRequest,
) -> EventSubscriptionRateCommandReceipt | Problem | None:
    """Configure rate bucket.

     Configure rate bucket: Durable per-subscription pacing shared by admission workers. Mutations
    require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a
    recorded rejection with no configuration change. Repeating an unexpired command UUID with identical
    inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second
    drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionRateRequest): Explicit guarded command for per-subscription token-
            bucket pacing. Disabled retains configuration without enforcing the bucket.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionRateCommandReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventSubscriptionRateRequest,
) -> Response[EventSubscriptionRateCommandReceipt | Problem]:
    """Configure rate bucket.

     Configure rate bucket: Durable per-subscription pacing shared by admission workers. Mutations
    require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a
    recorded rejection with no configuration change. Repeating an unexpired command UUID with identical
    inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second
    drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionRateRequest): Explicit guarded command for per-subscription token-
            bucket pacing. Disabled retains configuration without enforcing the bucket.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionRateCommandReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventSubscriptionRateRequest,
) -> EventSubscriptionRateCommandReceipt | Problem | None:
    """Configure rate bucket.

     Configure rate bucket: Durable per-subscription pacing shared by admission workers. Mutations
    require deploy:write or admin and return an applied or rejected receipt; revision mismatch is a
    recorded rejection with no configuration change. Repeating an unexpired command UUID with identical
    inputs returns its original receipt. Reads require read scope. Existing pause, circuit, per-second
    drain caps, attempts and delivery deadlines remain in force. Capacity observations reserve nothing.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionRateRequest): Explicit guarded command for per-subscription token-
            bucket pacing. Disabled retains configuration without enforcing the bucket.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionRateCommandReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            body=body,
        )
    ).parsed
