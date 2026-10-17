from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_cutover_preflight import ManagedPostgresCutoverPreflight
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/cutovers/{id}/preflight".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCutoverPreflight | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresCutoverPreflight.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresCutoverPreflight | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresCutoverPreflight | Problem]:
    """Read restore activation preflight findings

     Requires managed PostgreSQL read scope. Findings describe a historical
    catalog snapshot. Positive fields remain historical when stale is true.
    Staleness is recomputed for expiry, cutover changes and cancellation.
    Even fresh findings never authorize activation or prove writer isolation.
    Returns 404 if no preflight has been requested.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCutoverPreflight | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresCutoverPreflight | Problem | None:
    """Read restore activation preflight findings

     Requires managed PostgreSQL read scope. Findings describe a historical
    catalog snapshot. Positive fields remain historical when stale is true.
    Staleness is recomputed for expiry, cutover changes and cancellation.
    Even fresh findings never authorize activation or prove writer isolation.
    Returns 404 if no preflight has been requested.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCutoverPreflight | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresCutoverPreflight | Problem]:
    """Read restore activation preflight findings

     Requires managed PostgreSQL read scope. Findings describe a historical
    catalog snapshot. Positive fields remain historical when stale is true.
    Staleness is recomputed for expiry, cutover changes and cancellation.
    Even fresh findings never authorize activation or prove writer isolation.
    Returns 404 if no preflight has been requested.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCutoverPreflight | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresCutoverPreflight | Problem | None:
    """Read restore activation preflight findings

     Requires managed PostgreSQL read scope. Findings describe a historical
    catalog snapshot. Positive fields remain historical when stale is true.
    Staleness is recomputed for expiry, cutover changes and cancellation.
    Even fresh findings never authorize activation or prove writer isolation.
    Returns 404 if no preflight has been requested.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCutoverPreflight | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
