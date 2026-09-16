from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_running_response import DebugRunningResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: None | str | Unset
    if isinstance(since, Unset):
        json_since = UNSET
    else:
        json_since = since
    params["since"] = json_since

    json_limit: int | None | Unset
    if isinstance(limit, Unset):
        json_limit = UNSET
    else:
        json_limit = limit
    params["limit"] = json_limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/debug/running".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugRunningResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DebugRunningResponse.from_dict(response.json())

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
) -> Response[DebugRunningResponse | Problem]:
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
    limit: int | None | Unset = 20,
) -> Response[DebugRunningResponse | Problem]:
    """Explain why an app is still running.

     Returns observed scheduler causes that prevented an application from
    parking during the requested window. Causes are evidence-shaped: the
    response reports request activity, open connections, tail tasks,
    configured or temporary warm floors, cooldowns, and workload modes
    when those signals were observed. It does not estimate a saving or
    infer a protocol that was not instrumented. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugRunningResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        limit=limit,
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
    limit: int | None | Unset = 20,
) -> DebugRunningResponse | Problem | None:
    """Explain why an app is still running.

     Returns observed scheduler causes that prevented an application from
    parking during the requested window. Causes are evidence-shaped: the
    response reports request activity, open connections, tail tasks,
    configured or temporary warm floors, cooldowns, and workload modes
    when those signals were observed. It does not estimate a saving or
    infer a protocol that was not instrumented. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugRunningResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
) -> Response[DebugRunningResponse | Problem]:
    """Explain why an app is still running.

     Returns observed scheduler causes that prevented an application from
    parking during the requested window. Causes are evidence-shaped: the
    response reports request activity, open connections, tail tasks,
    configured or temporary warm floors, cooldowns, and workload modes
    when those signals were observed. It does not estimate a saving or
    infer a protocol that was not instrumented. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugRunningResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    limit: int | None | Unset = 20,
) -> DebugRunningResponse | Problem | None:
    """Explain why an app is still running.

     Returns observed scheduler causes that prevented an application from
    parking during the requested window. Causes are evidence-shaped: the
    response reports request activity, open connections, tail tasks,
    configured or temporary warm floors, cooldowns, and workload modes
    when those signals were observed. It does not estimate a saving or
    infer a protocol that was not instrumented. Plan-gated by
    `DebugTelemetryEnabled` and clamped to `DebugTelemetryRetentionDays`.

    Args:
        slug (str):
        since (None | str | Unset):
        limit (int | None | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugRunningResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            limit=limit,
        )
    ).parsed
