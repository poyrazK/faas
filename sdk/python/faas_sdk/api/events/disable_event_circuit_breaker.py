from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_circuit_breaker_response import EventCircuitBreakerResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/circuit-breaker".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventCircuitBreakerResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventCircuitBreakerResponse.from_dict(response.json())

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
) -> Response[EventCircuitBreakerResponse | Problem]:
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
) -> Response[EventCircuitBreakerResponse | Problem]:
    """Disable a consumer circuit breaker.

     Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one
    application consumer. Manual pauses remain independent. Invocation execution failures do not count.
    PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and
    manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown
    progresses when work is available.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventCircuitBreakerResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
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
) -> EventCircuitBreakerResponse | Problem | None:
    """Disable a consumer circuit breaker.

     Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one
    application consumer. Manual pauses remain independent. Invocation execution failures do not count.
    PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and
    manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown
    progresses when work is available.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventCircuitBreakerResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventCircuitBreakerResponse | Problem]:
    """Disable a consumer circuit breaker.

     Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one
    application consumer. Manual pauses remain independent. Invocation execution failures do not count.
    PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and
    manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown
    progresses when work is available.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventCircuitBreakerResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventCircuitBreakerResponse | Problem | None:
    """Disable a consumer circuit breaker.

     Requires `deploy:write` or `admin`. Runtime controls apply to retained pending routing for one
    application consumer. Manual pauses remain independent. Invocation execution failures do not count.
    PUT uses defaults for omitted fields and resets the observation window. Reset preserves policy and
    manual pause. Disable removes automatic gating. Status reports the last durable transition; cooldown
    progresses when work is available.

    Args:
        slug (str):
        subscription_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventCircuitBreakerResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
        )
    ).parsed
