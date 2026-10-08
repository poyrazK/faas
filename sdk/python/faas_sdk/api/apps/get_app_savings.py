import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_savings_response import AppSavingsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/savings".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppSavingsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppSavingsResponse.from_dict(response.json())

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
) -> Response[AppSavingsResponse | Problem]:
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[AppSavingsResponse | Problem]:
    """Per-app scale-to-zero savings estimate (trailing 30d by default).

     Estimates how much RAM-time and money scale-to-zero saved this
    app: billed usage compared with keeping `max(min_instances, 1)`
    instances running from the app's first billed hour in the
    window. Plan-gated Hobby+ with the same 402 as `/usage`.

    Window resolution matches `/usage` (RFC3339, UTC-midnight
    snaps, default trailing 30d), except `since` clamps to
    `until - 30d`: actual usage comes from `usage_minutes`
    (ADR-048, 30d retention), and a longer window would
    undercount it and overstate the saving.

    The figure is a conservative estimate, never an invoice line:
    companion sidecars are left out of the always-on baseline, and
    a burst above the baseline clamps the saving to zero.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSavingsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> AppSavingsResponse | Problem | None:
    """Per-app scale-to-zero savings estimate (trailing 30d by default).

     Estimates how much RAM-time and money scale-to-zero saved this
    app: billed usage compared with keeping `max(min_instances, 1)`
    instances running from the app's first billed hour in the
    window. Plan-gated Hobby+ with the same 402 as `/usage`.

    Window resolution matches `/usage` (RFC3339, UTC-midnight
    snaps, default trailing 30d), except `since` clamps to
    `until - 30d`: actual usage comes from `usage_minutes`
    (ADR-048, 30d retention), and a longer window would
    undercount it and overstate the saving.

    The figure is a conservative estimate, never an invoice line:
    companion sidecars are left out of the always-on baseline, and
    a burst above the baseline clamps the saving to zero.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSavingsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[AppSavingsResponse | Problem]:
    """Per-app scale-to-zero savings estimate (trailing 30d by default).

     Estimates how much RAM-time and money scale-to-zero saved this
    app: billed usage compared with keeping `max(min_instances, 1)`
    instances running from the app's first billed hour in the
    window. Plan-gated Hobby+ with the same 402 as `/usage`.

    Window resolution matches `/usage` (RFC3339, UTC-midnight
    snaps, default trailing 30d), except `since` clamps to
    `until - 30d`: actual usage comes from `usage_minutes`
    (ADR-048, 30d retention), and a longer window would
    undercount it and overstate the saving.

    The figure is a conservative estimate, never an invoice line:
    companion sidecars are left out of the always-on baseline, and
    a burst above the baseline clamps the saving to zero.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSavingsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> AppSavingsResponse | Problem | None:
    """Per-app scale-to-zero savings estimate (trailing 30d by default).

     Estimates how much RAM-time and money scale-to-zero saved this
    app: billed usage compared with keeping `max(min_instances, 1)`
    instances running from the app's first billed hour in the
    window. Plan-gated Hobby+ with the same 402 as `/usage`.

    Window resolution matches `/usage` (RFC3339, UTC-midnight
    snaps, default trailing 30d), except `since` clamps to
    `until - 30d`: actual usage comes from `usage_minutes`
    (ADR-048, 30d retention), and a longer window would
    undercount it and overstate the saving.

    The figure is a conservative estimate, never an invoice line:
    companion sidecars are left out of the always-on baseline, and
    a burst above the baseline clamps the saving to zero.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSavingsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
        )
    ).parsed
