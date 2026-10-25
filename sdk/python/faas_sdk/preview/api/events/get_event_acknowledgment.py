from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_acknowledgment_status import EventAcknowledgmentStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    invocation_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-deliveries/{invocation_id}/acknowledgment".format(
            slug=quote(str(slug), safe=""),
            invocation_id=quote(str(invocation_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventAcknowledgmentStatus | Problem | None:
    if response.status_code == 200:
        response_200 = EventAcknowledgmentStatus.from_dict(response.json())

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

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventAcknowledgmentStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventAcknowledgmentStatus | Problem]:
    """Inspect retained delivery processing state.

     Requires apps:read or admin. Tokens are never returned by inspection. Processing state distinguishes
    waiting, delivering, processing, expired and terminal invocation states. It does not prove business
    effects outside the consumer transaction.

    Args:
        slug (str):
        invocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        invocation_id=invocation_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventAcknowledgmentStatus | Problem | None:
    """Inspect retained delivery processing state.

     Requires apps:read or admin. Tokens are never returned by inspection. Processing state distinguishes
    waiting, delivering, processing, expired and terminal invocation states. It does not prove business
    effects outside the consumer transaction.

    Args:
        slug (str):
        invocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        invocation_id=invocation_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventAcknowledgmentStatus | Problem]:
    """Inspect retained delivery processing state.

     Requires apps:read or admin. Tokens are never returned by inspection. Processing state distinguishes
    waiting, delivering, processing, expired and terminal invocation states. It does not prove business
    effects outside the consumer transaction.

    Args:
        slug (str):
        invocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        invocation_id=invocation_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    invocation_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventAcknowledgmentStatus | Problem | None:
    """Inspect retained delivery processing state.

     Requires apps:read or admin. Tokens are never returned by inspection. Processing state distinguishes
    waiting, delivering, processing, expired and terminal invocation states. It does not prove business
    effects outside the consumer transaction.

    Args:
        slug (str):
        invocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            invocation_id=invocation_id,
            client=client,
        )
    ).parsed
