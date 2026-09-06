from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_telemetry_request_item import DebugTelemetryRequestItem
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    req_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/debug/requests/{req_id}".format(
            slug=quote(str(slug), safe=""),
            req_id=quote(str(req_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugTelemetryRequestItem | Problem | None:
    if response.status_code == 200:
        response_200 = DebugTelemetryRequestItem.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DebugTelemetryRequestItem | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DebugTelemetryRequestItem | Problem]:
    """Get one request telemetry record (ADR-127).

     Returns one request telemetry row by id for the app. The
    lookup is scoped to the app resolved from `slug`, so a request
    id belonging to another app is returned as not found. This
    direct lookup is not limited to the first page of recent
    requests. Plan-gated by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        req_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugTelemetryRequestItem | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        req_id=req_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> DebugTelemetryRequestItem | Problem | None:
    """Get one request telemetry record (ADR-127).

     Returns one request telemetry row by id for the app. The
    lookup is scoped to the app resolved from `slug`, so a request
    id belonging to another app is returned as not found. This
    direct lookup is not limited to the first page of recent
    requests. Plan-gated by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        req_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugTelemetryRequestItem | Problem
    """

    return sync_detailed(
        slug=slug,
        req_id=req_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DebugTelemetryRequestItem | Problem]:
    """Get one request telemetry record (ADR-127).

     Returns one request telemetry row by id for the app. The
    lookup is scoped to the app resolved from `slug`, so a request
    id belonging to another app is returned as not found. This
    direct lookup is not limited to the first page of recent
    requests. Plan-gated by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        req_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugTelemetryRequestItem | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        req_id=req_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> DebugTelemetryRequestItem | Problem | None:
    """Get one request telemetry record (ADR-127).

     Returns one request telemetry row by id for the app. The
    lookup is scoped to the app resolved from `slug`, so a request
    id belonging to another app is returned as not found. This
    direct lookup is not limited to the first page of recent
    requests. Plan-gated by `DebugTelemetryEnabled`.

    Args:
        slug (str):
        req_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugTelemetryRequestItem | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            req_id=req_id,
            client=client,
        )
    ).parsed
