import datetime
from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.stream_app_logs_follow import StreamAppLogsFollow
from ...models.stream_app_logs_level import StreamAppLogsLevel
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    follow: StreamAppLogsFollow | Unset = 0,
    grep: str | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    level: StreamAppLogsLevel | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_follow: int | Unset = UNSET
    if not isinstance(follow, Unset):
        json_follow = follow

    params["follow"] = json_follow

    params["grep"] = grep

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_level: str | Unset = UNSET
    if not isinstance(level, Unset):
        json_level = level

    params["level"] = json_level

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/logs".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 200:
        response_200 = cast(Any, None)
        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
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
    follow: StreamAppLogsFollow | Unset = 0,
    grep: str | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    level: StreamAppLogsLevel | Unset = UNSET,
) -> Response[Any | Problem]:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        follow=follow,
        grep=grep,
        since=since,
        level=level,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    follow: StreamAppLogsFollow | Unset = 0,
    grep: str | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    level: StreamAppLogsLevel | Unset = UNSET,
) -> Any | Problem | None:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        follow=follow,
        grep=grep,
        since=since,
        level=level,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    follow: StreamAppLogsFollow | Unset = 0,
    grep: str | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    level: StreamAppLogsLevel | Unset = UNSET,
) -> Response[Any | Problem]:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        follow=follow,
        grep=grep,
        since=since,
        level=level,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    follow: StreamAppLogsFollow | Unset = 0,
    grep: str | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    level: StreamAppLogsLevel | Unset = UNSET,
) -> Any | Problem | None:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            follow=follow,
            grep=grep,
            since=since,
            level=level,
        )
    ).parsed
