from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_dependency_latency_response import DebugDependencyLatencyResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: None | str | Unset = UNSET,
) -> dict[str, Any]:
    params: dict[str, Any] = {}

    json_since: None | str | Unset
    if isinstance(since, Unset):
        json_since = UNSET
    else:
        json_since = since
    params["since"] = json_since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/debug/dependencies".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugDependencyLatencyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DebugDependencyLatencyResponse.from_dict(response.json())

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
) -> Response[DebugDependencyLatencyResponse | Problem]:
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
    since: None | str | Unset = UNSET,
) -> Response[DebugDependencyLatencyResponse | Problem]:
    """Historical dependency latency and regressions.

     Returns bounded dependency percentiles and error rates derived from
    retained, redacted span summaries. The result compares the newer and
    older halves of the selected window to flag a dependency regression.
    Raw span attributes, destinations, request bodies, and credentials
    are never returned. Span evidence is sampled and the response marks
    row/cardinality truncation explicitly. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugDependencyLatencyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
) -> DebugDependencyLatencyResponse | Problem | None:
    """Historical dependency latency and regressions.

     Returns bounded dependency percentiles and error rates derived from
    retained, redacted span summaries. The result compares the newer and
    older halves of the selected window to flag a dependency regression.
    Raw span attributes, destinations, request bodies, and credentials
    are never returned. Span evidence is sampled and the response marks
    row/cardinality truncation explicitly. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugDependencyLatencyResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
) -> Response[DebugDependencyLatencyResponse | Problem]:
    """Historical dependency latency and regressions.

     Returns bounded dependency percentiles and error rates derived from
    retained, redacted span summaries. The result compares the newer and
    older halves of the selected window to flag a dependency regression.
    Raw span attributes, destinations, request bodies, and credentials
    are never returned. Span evidence is sampled and the response marks
    row/cardinality truncation explicitly. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugDependencyLatencyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
) -> DebugDependencyLatencyResponse | Problem | None:
    """Historical dependency latency and regressions.

     Returns bounded dependency percentiles and error rates derived from
    retained, redacted span summaries. The result compares the newer and
    older halves of the selected window to flag a dependency regression.
    Raw span attributes, destinations, request bodies, and credentials
    are never returned. Span evidence is sampled and the response marks
    row/cardinality truncation explicitly. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugDependencyLatencyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
        )
    ).parsed
