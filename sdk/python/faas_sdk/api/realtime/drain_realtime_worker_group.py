from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_consumer_group_drain_state import RealtimeConsumerGroupDrainState
from ...types import Response


def _get_kwargs(
    group_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/realtime/workers/groups/{group_id}/drain".format(
            group_id=quote(str(group_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RealtimeConsumerGroupDrainState | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupDrainState.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RealtimeConsumerGroupDrainState]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    group_id: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | RealtimeConsumerGroupDrainState]:
    """Drain active consumer-group leases using scoped worker credentials

     Drain active consumer-group leases. Idempotently stop new claims and enter persistent drain mode.
    Requires account admin scope and MFA, or the explicit drain worker permission. Existing leases may
    renew or settle. Drained means no unexpired leases remain; it does not prove handler side effects
    have stopped. Retry/dead/unassigned work is preserved. Resume explicitly after deployment; pause
    switches to ordinary paused mode.

    Args:
        group_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeConsumerGroupDrainState]
    """

    kwargs = _get_kwargs(
        group_id=group_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    group_id: str,
    *,
    client: AuthenticatedClient,
) -> Problem | RealtimeConsumerGroupDrainState | None:
    """Drain active consumer-group leases using scoped worker credentials

     Drain active consumer-group leases. Idempotently stop new claims and enter persistent drain mode.
    Requires account admin scope and MFA, or the explicit drain worker permission. Existing leases may
    renew or settle. Drained means no unexpired leases remain; it does not prove handler side effects
    have stopped. Retry/dead/unassigned work is preserved. Resume explicitly after deployment; pause
    switches to ordinary paused mode.

    Args:
        group_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeConsumerGroupDrainState
    """

    return sync_detailed(
        group_id=group_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    group_id: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | RealtimeConsumerGroupDrainState]:
    """Drain active consumer-group leases using scoped worker credentials

     Drain active consumer-group leases. Idempotently stop new claims and enter persistent drain mode.
    Requires account admin scope and MFA, or the explicit drain worker permission. Existing leases may
    renew or settle. Drained means no unexpired leases remain; it does not prove handler side effects
    have stopped. Retry/dead/unassigned work is preserved. Resume explicitly after deployment; pause
    switches to ordinary paused mode.

    Args:
        group_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeConsumerGroupDrainState]
    """

    kwargs = _get_kwargs(
        group_id=group_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    group_id: str,
    *,
    client: AuthenticatedClient,
) -> Problem | RealtimeConsumerGroupDrainState | None:
    """Drain active consumer-group leases using scoped worker credentials

     Drain active consumer-group leases. Idempotently stop new claims and enter persistent drain mode.
    Requires account admin scope and MFA, or the explicit drain worker permission. Existing leases may
    renew or settle. Drained means no unexpired leases remain; it does not prove handler side effects
    have stopped. Retry/dead/unassigned work is preserved. Resume explicitly after deployment; pause
    switches to ordinary paused mode.

    Args:
        group_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeConsumerGroupDrainState
    """

    return (
        await asyncio_detailed(
            group_id=group_id,
            client=client,
        )
    ).parsed
