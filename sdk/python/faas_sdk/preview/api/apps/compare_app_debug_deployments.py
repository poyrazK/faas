from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_compare_request import DebugCompareRequest
from ...models.debug_compare_response import DebugCompareResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DebugCompareRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/debug/compare".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugCompareResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DebugCompareResponse.from_dict(response.json())

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
) -> Response[DebugCompareResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DebugCompareRequest,
) -> Response[DebugCompareResponse | Problem]:
    """Per-route latency compare.

     Compares two deployments' per-route latency
    distributions in a shared time window. Body holds the
    two deployment ids + optional route filter + optional
    since/until bounds. Returns merged per-route stats with
    per-deployment p50/p95/p99 + represented request counts. Includes
    the effective window and retention-clamping metadata.
    Invalid lookbacks, invalid/future until values, and windows wholly
    outside current plan retention are rejected. Reads retained telemetry
    without replaying requests or writing platform state. Plan-gated by
    DebugTelemetryEnabled.

    Args:
        slug (str):
        body (DebugCompareRequest): POST body for /v1/apps/{slug}/debug/compare.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugCompareResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DebugCompareRequest,
) -> DebugCompareResponse | Problem | None:
    """Per-route latency compare.

     Compares two deployments' per-route latency
    distributions in a shared time window. Body holds the
    two deployment ids + optional route filter + optional
    since/until bounds. Returns merged per-route stats with
    per-deployment p50/p95/p99 + represented request counts. Includes
    the effective window and retention-clamping metadata.
    Invalid lookbacks, invalid/future until values, and windows wholly
    outside current plan retention are rejected. Reads retained telemetry
    without replaying requests or writing platform state. Plan-gated by
    DebugTelemetryEnabled.

    Args:
        slug (str):
        body (DebugCompareRequest): POST body for /v1/apps/{slug}/debug/compare.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugCompareResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DebugCompareRequest,
) -> Response[DebugCompareResponse | Problem]:
    """Per-route latency compare.

     Compares two deployments' per-route latency
    distributions in a shared time window. Body holds the
    two deployment ids + optional route filter + optional
    since/until bounds. Returns merged per-route stats with
    per-deployment p50/p95/p99 + represented request counts. Includes
    the effective window and retention-clamping metadata.
    Invalid lookbacks, invalid/future until values, and windows wholly
    outside current plan retention are rejected. Reads retained telemetry
    without replaying requests or writing platform state. Plan-gated by
    DebugTelemetryEnabled.

    Args:
        slug (str):
        body (DebugCompareRequest): POST body for /v1/apps/{slug}/debug/compare.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugCompareResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DebugCompareRequest,
) -> DebugCompareResponse | Problem | None:
    """Per-route latency compare.

     Compares two deployments' per-route latency
    distributions in a shared time window. Body holds the
    two deployment ids + optional route filter + optional
    since/until bounds. Returns merged per-route stats with
    per-deployment p50/p95/p99 + represented request counts. Includes
    the effective window and retention-clamping metadata.
    Invalid lookbacks, invalid/future until values, and windows wholly
    outside current plan retention are rejected. Reads retained telemetry
    without replaying requests or writing platform state. Plan-gated by
    DebugTelemetryEnabled.

    Args:
        slug (str):
        body (DebugCompareRequest): POST body for /v1/apps/{slug}/debug/compare.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugCompareResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
