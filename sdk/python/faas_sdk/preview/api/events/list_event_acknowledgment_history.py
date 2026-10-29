from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_acknowledgment_history import EventAcknowledgmentHistory
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    invocation_id: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_invocation_id: str | Unset = UNSET
    if not isinstance(invocation_id, Unset):
        json_invocation_id = str(invocation_id)
    params["invocation_id"] = json_invocation_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/acknowledgment-history".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventAcknowledgmentHistory | Problem | None:
    if response.status_code == 200:
        response_200 = EventAcknowledgmentHistory.from_dict(response.json())

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
) -> Response[EventAcknowledgmentHistory | Problem]:
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
    invocation_id: UUID | Unset = UNSET,
) -> Response[EventAcknowledgmentHistory | Problem]:
    """Inspect retained deferred acknowledgment outcomes.

     Requires apps:read or admin. Returns the 100 most recently updated outcome groups for this consumer,
    optionally restricted to one delivery. Repeated operations coalesce per attempt and kind;
    occurrences saturates at 2147483647. Retention ends seven days after the attempt deadline, or
    earlier when the invocation is deleted. Invalid and replaced tokens produce no history. This is
    diagnostic coverage since rollout, not a complete audit log or a rate denominator.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentHistory | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        invocation_id=invocation_id,
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
    invocation_id: UUID | Unset = UNSET,
) -> EventAcknowledgmentHistory | Problem | None:
    """Inspect retained deferred acknowledgment outcomes.

     Requires apps:read or admin. Returns the 100 most recently updated outcome groups for this consumer,
    optionally restricted to one delivery. Repeated operations coalesce per attempt and kind;
    occurrences saturates at 2147483647. Retention ends seven days after the attempt deadline, or
    earlier when the invocation is deleted. Invalid and replaced tokens produce no history. This is
    diagnostic coverage since rollout, not a complete audit log or a rate denominator.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentHistory | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        invocation_id=invocation_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    invocation_id: UUID | Unset = UNSET,
) -> Response[EventAcknowledgmentHistory | Problem]:
    """Inspect retained deferred acknowledgment outcomes.

     Requires apps:read or admin. Returns the 100 most recently updated outcome groups for this consumer,
    optionally restricted to one delivery. Repeated operations coalesce per attempt and kind;
    occurrences saturates at 2147483647. Retention ends seven days after the attempt deadline, or
    earlier when the invocation is deleted. Invalid and replaced tokens produce no history. This is
    diagnostic coverage since rollout, not a complete audit log or a rate denominator.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventAcknowledgmentHistory | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        invocation_id=invocation_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    invocation_id: UUID | Unset = UNSET,
) -> EventAcknowledgmentHistory | Problem | None:
    """Inspect retained deferred acknowledgment outcomes.

     Requires apps:read or admin. Returns the 100 most recently updated outcome groups for this consumer,
    optionally restricted to one delivery. Repeated operations coalesce per attempt and kind;
    occurrences saturates at 2147483647. Retention ends seven days after the attempt deadline, or
    earlier when the invocation is deleted. Invalid and replaced tokens produce no history. This is
    diagnostic coverage since rollout, not a complete audit log or a rate denominator.

    Args:
        slug (str):
        subscription_id (UUID):
        invocation_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventAcknowledgmentHistory | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            invocation_id=invocation_id,
        )
    ).parsed
