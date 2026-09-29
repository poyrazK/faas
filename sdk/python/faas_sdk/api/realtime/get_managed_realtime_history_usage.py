from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_history_usage_response import ManagedRealtimeHistoryUsageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/realtime-history-usage",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeHistoryUsageResponse | Problem:
    if response.status_code == 200:
        response_200 = ManagedRealtimeHistoryUsageResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedRealtimeHistoryUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimeHistoryUsageResponse | Problem]:
    """Read the account's retained realtime history snapshot

     Preview only; requires usage read scope and MFA for interactive sessions.
    Counts current retained message rows and payload bytes for this account,
    including expired rows awaiting cleanup. Replayable counts apply the
    channel's contiguous retention floor. These values are informational
    snapshots, not billed usage or physical database allocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeHistoryUsageResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimeHistoryUsageResponse | Problem | None:
    """Read the account's retained realtime history snapshot

     Preview only; requires usage read scope and MFA for interactive sessions.
    Counts current retained message rows and payload bytes for this account,
    including expired rows awaiting cleanup. Replayable counts apply the
    channel's contiguous retention floor. These values are informational
    snapshots, not billed usage or physical database allocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeHistoryUsageResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimeHistoryUsageResponse | Problem]:
    """Read the account's retained realtime history snapshot

     Preview only; requires usage read scope and MFA for interactive sessions.
    Counts current retained message rows and payload bytes for this account,
    including expired rows awaiting cleanup. Replayable counts apply the
    channel's contiguous retention floor. These values are informational
    snapshots, not billed usage or physical database allocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeHistoryUsageResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimeHistoryUsageResponse | Problem | None:
    """Read the account's retained realtime history snapshot

     Preview only; requires usage read scope and MFA for interactive sessions.
    Counts current retained message rows and payload bytes for this account,
    including expired rows awaiting cleanup. Replayable counts apply the
    channel's contiguous retention floor. These values are informational
    snapshots, not billed usage or physical database allocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeHistoryUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
