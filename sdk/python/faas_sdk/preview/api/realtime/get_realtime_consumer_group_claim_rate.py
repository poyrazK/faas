from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_claim_rate_status import RealtimeConsumerGroupClaimRateStatus
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | RealtimeConsumerGroupClaimRateStatus | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupClaimRateStatus.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | Problem | RealtimeConsumerGroupClaimRateStatus]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem | RealtimeConsumerGroupClaimRateStatus]:
    """Read shared consumer-group claim rate

     Requires app ownership and MFA; writes require admin scope. Zero rate disables throttling. Otherwise
    configure 1 to 1000 claims per second and a burst of 1 to 32. Admission is shared across every
    worker and replica through persisted group-locked state. Renewals, settlements and replay do not
    consume rate credit. Identical writes preserve credit and counters; changed policies clamp existing
    debt to the new burst window. The observed retry hint is not a reservation; concurrent workers can
    consume the next allowance.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupClaimRateStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | RealtimeConsumerGroupClaimRateStatus | None:
    """Read shared consumer-group claim rate

     Requires app ownership and MFA; writes require admin scope. Zero rate disables throttling. Otherwise
    configure 1 to 1000 claims per second and a burst of 1 to 32. Admission is shared across every
    worker and replica through persisted group-locked state. Renewals, settlements and replay do not
    consume rate credit. Identical writes preserve credit and counters; changed policies clamp existing
    debt to the new burst window. The observed retry hint is not a reservation; concurrent workers can
    consume the next allowance.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupClaimRateStatus
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem | RealtimeConsumerGroupClaimRateStatus]:
    """Read shared consumer-group claim rate

     Requires app ownership and MFA; writes require admin scope. Zero rate disables throttling. Otherwise
    configure 1 to 1000 claims per second and a burst of 1 to 32. Admission is shared across every
    worker and replica through persisted group-locked state. Renewals, settlements and replay do not
    consume rate credit. Identical writes preserve credit and counters; changed policies clamp existing
    debt to the new burst window. The observed retry hint is not a reservation; concurrent workers can
    consume the next allowance.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RealtimeConsumerGroupClaimRateStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | RealtimeConsumerGroupClaimRateStatus | None:
    """Read shared consumer-group claim rate

     Requires app ownership and MFA; writes require admin scope. Zero rate disables throttling. Otherwise
    configure 1 to 1000 claims per second and a burst of 1 to 32. Admission is shared across every
    worker and replica through persisted group-locked state. Renewals, settlements and replay do not
    consume rate credit. Identical writes preserve credit and counters; changed policies clamp existing
    debt to the new burst window. The observed retry hint is not a reservation; concurrent workers can
    consume the next allowance.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RealtimeConsumerGroupClaimRateStatus
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            client=client,
        )
    ).parsed
