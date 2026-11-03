from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_usage_response import RealtimeUsageResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    hours: int | Unset = 24,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["hours"] = hours

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/usage/realtime",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RealtimeUsageResponse | None:
    if response.status_code == 200:
        response_200 = RealtimeUsageResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RealtimeUsageResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    hours: int | Unset = 24,
) -> Response[Problem | RealtimeUsageResponse]:
    """Realtime connections against the plan's fair-use limit, and recorded usage per hour.

     Realtime is free. open_connections counts connections on realtime nodes that reported in the last 30
    seconds. Hourly usage appears only when the operator records realtime usage; the peak is the most
    connections open at once in any minute of the hour.

    Args:
        hours (int | Unset):  Default: 24.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeUsageResponse]
    """

    kwargs = _get_kwargs(
        hours=hours,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    hours: int | Unset = 24,
) -> Problem | RealtimeUsageResponse | None:
    """Realtime connections against the plan's fair-use limit, and recorded usage per hour.

     Realtime is free. open_connections counts connections on realtime nodes that reported in the last 30
    seconds. Hourly usage appears only when the operator records realtime usage; the peak is the most
    connections open at once in any minute of the hour.

    Args:
        hours (int | Unset):  Default: 24.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeUsageResponse
    """

    return sync_detailed(
        client=client,
        hours=hours,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    hours: int | Unset = 24,
) -> Response[Problem | RealtimeUsageResponse]:
    """Realtime connections against the plan's fair-use limit, and recorded usage per hour.

     Realtime is free. open_connections counts connections on realtime nodes that reported in the last 30
    seconds. Hourly usage appears only when the operator records realtime usage; the peak is the most
    connections open at once in any minute of the hour.

    Args:
        hours (int | Unset):  Default: 24.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeUsageResponse]
    """

    kwargs = _get_kwargs(
        hours=hours,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    hours: int | Unset = 24,
) -> Problem | RealtimeUsageResponse | None:
    """Realtime connections against the plan's fair-use limit, and recorded usage per hour.

     Realtime is free. open_connections counts connections on realtime nodes that reported in the last 30
    seconds. Hourly usage appears only when the operator records realtime usage; the peak is the most
    connections open at once in any minute of the hour.

    Args:
        hours (int | Unset):  Default: 24.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeUsageResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            hours=hours,
        )
    ).parsed
