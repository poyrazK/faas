import datetime
from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.stream_app_logs_archive import StreamAppLogsArchive
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
    archive: StreamAppLogsArchive | Unset = 0,
    instance: str | Unset = UNSET,
    date: datetime.date | Unset = UNSET,
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

    json_archive: int | Unset = UNSET
    if not isinstance(archive, Unset):
        json_archive = archive

    params["archive"] = json_archive

    params["instance"] = instance

    json_date: str | Unset = UNSET
    if not isinstance(date, Unset):
        json_date = date.isoformat()
    params["date"] = json_date

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

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
    archive: StreamAppLogsArchive | Unset = 0,
    instance: str | Unset = UNSET,
    date: datetime.date | Unset = UNSET,
) -> Response[Any | Problem]:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Two modes share this URL:

    - **Live (default)** — `?follow=1` holds the connection open and
      streams new entries from the per-instance ring buffer. The
      stream terminates with `event: end` when the backstop fires
      (10 minutes idle), the schedd returns NotFound (parked app), or
      the connection closes.

    - **Archive (`?archive=1`)** — fetches a single day's
      per-instance log batch from the S3 bucket the apid shipper
      writes into. `?instance=<id>` selects the Firecracker instance
      id; `?date=YYYY-MM-DD` selects the day. The response is the
      same SSE shape as the live stream (`event: log` per line,
      `event: end` terminal with `archive_complete` /
      `archive_missing` / `archive_degraded` reasons) so the SDK
      decoder treats the two paths interchangeably. Archive is
      gated by `Plan.LogArchiveEnabled()` — Free customers receive a
      one-day archive window. The per-plan retention cap (Free 1d /
      Hobby 7d / Pro 30d / Scale 90d) refuses `?date=` values
      outside the window with 403 + `log_archive_retention_exceeded`.

    Each `event: log` payload preserves the original `line`. When that
    line is a valid JSON object with a recognized `level` or `severity`
    field, the server adds a canonical `level` value (`info`, `warn`, or
    `error`); plain-text and unclassified lines omit the field.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):
        archive (StreamAppLogsArchive | Unset):  Default: 0.
        instance (str | Unset):
        date (datetime.date | Unset):

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
        archive=archive,
        instance=instance,
        date=date,
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
    archive: StreamAppLogsArchive | Unset = 0,
    instance: str | Unset = UNSET,
    date: datetime.date | Unset = UNSET,
) -> Any | Problem | None:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Two modes share this URL:

    - **Live (default)** — `?follow=1` holds the connection open and
      streams new entries from the per-instance ring buffer. The
      stream terminates with `event: end` when the backstop fires
      (10 minutes idle), the schedd returns NotFound (parked app), or
      the connection closes.

    - **Archive (`?archive=1`)** — fetches a single day's
      per-instance log batch from the S3 bucket the apid shipper
      writes into. `?instance=<id>` selects the Firecracker instance
      id; `?date=YYYY-MM-DD` selects the day. The response is the
      same SSE shape as the live stream (`event: log` per line,
      `event: end` terminal with `archive_complete` /
      `archive_missing` / `archive_degraded` reasons) so the SDK
      decoder treats the two paths interchangeably. Archive is
      gated by `Plan.LogArchiveEnabled()` — Free customers receive a
      one-day archive window. The per-plan retention cap (Free 1d /
      Hobby 7d / Pro 30d / Scale 90d) refuses `?date=` values
      outside the window with 403 + `log_archive_retention_exceeded`.

    Each `event: log` payload preserves the original `line`. When that
    line is a valid JSON object with a recognized `level` or `severity`
    field, the server adds a canonical `level` value (`info`, `warn`, or
    `error`); plain-text and unclassified lines omit the field.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):
        archive (StreamAppLogsArchive | Unset):  Default: 0.
        instance (str | Unset):
        date (datetime.date | Unset):

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
        archive=archive,
        instance=instance,
        date=date,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    follow: StreamAppLogsFollow | Unset = 0,
    grep: str | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    level: StreamAppLogsLevel | Unset = UNSET,
    archive: StreamAppLogsArchive | Unset = 0,
    instance: str | Unset = UNSET,
    date: datetime.date | Unset = UNSET,
) -> Response[Any | Problem]:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Two modes share this URL:

    - **Live (default)** — `?follow=1` holds the connection open and
      streams new entries from the per-instance ring buffer. The
      stream terminates with `event: end` when the backstop fires
      (10 minutes idle), the schedd returns NotFound (parked app), or
      the connection closes.

    - **Archive (`?archive=1`)** — fetches a single day's
      per-instance log batch from the S3 bucket the apid shipper
      writes into. `?instance=<id>` selects the Firecracker instance
      id; `?date=YYYY-MM-DD` selects the day. The response is the
      same SSE shape as the live stream (`event: log` per line,
      `event: end` terminal with `archive_complete` /
      `archive_missing` / `archive_degraded` reasons) so the SDK
      decoder treats the two paths interchangeably. Archive is
      gated by `Plan.LogArchiveEnabled()` — Free customers receive a
      one-day archive window. The per-plan retention cap (Free 1d /
      Hobby 7d / Pro 30d / Scale 90d) refuses `?date=` values
      outside the window with 403 + `log_archive_retention_exceeded`.

    Each `event: log` payload preserves the original `line`. When that
    line is a valid JSON object with a recognized `level` or `severity`
    field, the server adds a canonical `level` value (`info`, `warn`, or
    `error`); plain-text and unclassified lines omit the field.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):
        archive (StreamAppLogsArchive | Unset):  Default: 0.
        instance (str | Unset):
        date (datetime.date | Unset):

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
        archive=archive,
        instance=instance,
        date=date,
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
    archive: StreamAppLogsArchive | Unset = 0,
    instance: str | Unset = UNSET,
    date: datetime.date | Unset = UNSET,
) -> Any | Problem | None:
    """Stream app logs (SSE).

     Server-Sent Events stream of instance logs. NOTE: this endpoint is
    currently mounted behind `s.authLimited` and is documented here for
    reference; the dashboard and CLI also consume it directly.

    Two modes share this URL:

    - **Live (default)** — `?follow=1` holds the connection open and
      streams new entries from the per-instance ring buffer. The
      stream terminates with `event: end` when the backstop fires
      (10 minutes idle), the schedd returns NotFound (parked app), or
      the connection closes.

    - **Archive (`?archive=1`)** — fetches a single day's
      per-instance log batch from the S3 bucket the apid shipper
      writes into. `?instance=<id>` selects the Firecracker instance
      id; `?date=YYYY-MM-DD` selects the day. The response is the
      same SSE shape as the live stream (`event: log` per line,
      `event: end` terminal with `archive_complete` /
      `archive_missing` / `archive_degraded` reasons) so the SDK
      decoder treats the two paths interchangeably. Archive is
      gated by `Plan.LogArchiveEnabled()` — Free customers receive a
      one-day archive window. The per-plan retention cap (Free 1d /
      Hobby 7d / Pro 30d / Scale 90d) refuses `?date=` values
      outside the window with 403 + `log_archive_retention_exceeded`.

    Each `event: log` payload preserves the original `line`. When that
    line is a valid JSON object with a recognized `level` or `severity`
    field, the server adds a canonical `level` value (`info`, `warn`, or
    `error`); plain-text and unclassified lines omit the field.

    Args:
        slug (str):
        follow (StreamAppLogsFollow | Unset):  Default: 0.
        grep (str | Unset):
        since (datetime.datetime | Unset):
        level (StreamAppLogsLevel | Unset):
        archive (StreamAppLogsArchive | Unset):  Default: 0.
        instance (str | Unset):
        date (datetime.date | Unset):

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
            archive=archive,
            instance=instance,
            date=date,
        )
    ).parsed
