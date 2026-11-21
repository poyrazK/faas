from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_events_request import FlagEventsRequest
from ...models.flag_events_response import FlagEventsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: FlagEventsRequest,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(x_faas_flags_capabilities, Unset):
        headers["X-Faas-Flags-Capabilities"] = x_faas_flags_capabilities

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/flags/events",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagEventsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FlagEventsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FlagEventsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: FlagEventsRequest,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> Response[FlagEventsResponse | Problem]:
    """Report conversion events for flag experiments.

     Requires the same workload identity as getRuntimeFlags. Account, app and environment come from the
    token. Each event names the verified customer of the request it belongs to; events for customers
    that are not active platform tenants of the account are dropped. Accepted events count against the
    plan's monthly event quota (FlagEventsPerMonth); events over quota are dropped and counted, never
    billed.

    Args:
        x_faas_flags_capabilities (str | Unset):
        body (FlagEventsRequest): A batch of runtime conversion events.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        x_faas_flags_capabilities=x_faas_flags_capabilities,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: FlagEventsRequest,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> FlagEventsResponse | Problem | None:
    """Report conversion events for flag experiments.

     Requires the same workload identity as getRuntimeFlags. Account, app and environment come from the
    token. Each event names the verified customer of the request it belongs to; events for customers
    that are not active platform tenants of the account are dropped. Accepted events count against the
    plan's monthly event quota (FlagEventsPerMonth); events over quota are dropped and counted, never
    billed.

    Args:
        x_faas_flags_capabilities (str | Unset):
        body (FlagEventsRequest): A batch of runtime conversion events.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagEventsResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        x_faas_flags_capabilities=x_faas_flags_capabilities,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: FlagEventsRequest,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> Response[FlagEventsResponse | Problem]:
    """Report conversion events for flag experiments.

     Requires the same workload identity as getRuntimeFlags. Account, app and environment come from the
    token. Each event names the verified customer of the request it belongs to; events for customers
    that are not active platform tenants of the account are dropped. Accepted events count against the
    plan's monthly event quota (FlagEventsPerMonth); events over quota are dropped and counted, never
    billed.

    Args:
        x_faas_flags_capabilities (str | Unset):
        body (FlagEventsRequest): A batch of runtime conversion events.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        x_faas_flags_capabilities=x_faas_flags_capabilities,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: FlagEventsRequest,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> FlagEventsResponse | Problem | None:
    """Report conversion events for flag experiments.

     Requires the same workload identity as getRuntimeFlags. Account, app and environment come from the
    token. Each event names the verified customer of the request it belongs to; events for customers
    that are not active platform tenants of the account are dropped. Accepted events count against the
    plan's monthly event quota (FlagEventsPerMonth); events over quota are dropped and counted, never
    billed.

    Args:
        x_faas_flags_capabilities (str | Unset):
        body (FlagEventsRequest): A batch of runtime conversion events.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_faas_flags_capabilities=x_faas_flags_capabilities,
        )
    ).parsed
