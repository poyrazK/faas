from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.execution_environment_events_response import ExecutionEnvironmentEventsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    after: int | Unset = 0,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after"] = after

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/execution-environments/{id}/events".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExecutionEnvironmentEventsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ExecutionEnvironmentEventsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExecutionEnvironmentEventsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
) -> Response[ExecutionEnvironmentEventsResponse | Problem]:
    """Read resumable, bounded dependency-build diagnostics.

     Uses environment ownership. Poll during a build; sequences survive API restarts. A gap explicitly
    reports an evicted retained tail. Reads remain available after writer gates are disabled.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionEnvironmentEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        after=after,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
) -> ExecutionEnvironmentEventsResponse | Problem | None:
    """Read resumable, bounded dependency-build diagnostics.

     Uses environment ownership. Poll during a build; sequences survive API restarts. A gap explicitly
    reports an evicted retained tail. Reads remain available after writer gates are disabled.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionEnvironmentEventsResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        after=after,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
) -> Response[ExecutionEnvironmentEventsResponse | Problem]:
    """Read resumable, bounded dependency-build diagnostics.

     Uses environment ownership. Poll during a build; sequences survive API restarts. A gap explicitly
    reports an evicted retained tail. Reads remain available after writer gates are disabled.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionEnvironmentEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        after=after,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
) -> ExecutionEnvironmentEventsResponse | Problem | None:
    """Read resumable, bounded dependency-build diagnostics.

     Uses environment ownership. Poll during a build; sequences survive API restarts. A gap explicitly
    reports an evicted retained tail. Reads remain available after writer gates are disabled.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionEnvironmentEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            after=after,
        )
    ).parsed
