from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.seat_usage_response import SeatUsageResponse
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/seat_usage".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SeatUsageResponse | None:
    if response.status_code == 200:
        response_200 = SeatUsageResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SeatUsageResponse]:
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
) -> Response[Problem | SeatUsageResponse]:
    r"""Seat usage visibility for the active org.

     Returns `{used, limit, plan}` from `Store.CountActiveOrgMembers`
    (the same row the cap-in-tx inside `ConsumeOrgInvitation`
    reads). `limit` comes from `org.Plan.OrgMembersMax()` — the
    `free` plan returns `0` to render \"personal org only\" in the
    dashboard rather than \"0 of 0 used\". Visibility-only; PR 9
    ships the per-seat pricing cut-over per ADR-061 §\"Out of
    scope\". Every role may read (gated by `org.view`).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SeatUsageResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | SeatUsageResponse | None:
    r"""Seat usage visibility for the active org.

     Returns `{used, limit, plan}` from `Store.CountActiveOrgMembers`
    (the same row the cap-in-tx inside `ConsumeOrgInvitation`
    reads). `limit` comes from `org.Plan.OrgMembersMax()` — the
    `free` plan returns `0` to render \"personal org only\" in the
    dashboard rather than \"0 of 0 used\". Visibility-only; PR 9
    ships the per-seat pricing cut-over per ADR-061 §\"Out of
    scope\". Every role may read (gated by `org.view`).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SeatUsageResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | SeatUsageResponse]:
    r"""Seat usage visibility for the active org.

     Returns `{used, limit, plan}` from `Store.CountActiveOrgMembers`
    (the same row the cap-in-tx inside `ConsumeOrgInvitation`
    reads). `limit` comes from `org.Plan.OrgMembersMax()` — the
    `free` plan returns `0` to render \"personal org only\" in the
    dashboard rather than \"0 of 0 used\". Visibility-only; PR 9
    ships the per-seat pricing cut-over per ADR-061 §\"Out of
    scope\". Every role may read (gated by `org.view`).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SeatUsageResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | SeatUsageResponse | None:
    r"""Seat usage visibility for the active org.

     Returns `{used, limit, plan}` from `Store.CountActiveOrgMembers`
    (the same row the cap-in-tx inside `ConsumeOrgInvitation`
    reads). `limit` comes from `org.Plan.OrgMembersMax()` — the
    `free` plan returns `0` to render \"personal org only\" in the
    dashboard rather than \"0 of 0 used\". Visibility-only; PR 9
    ships the per-seat pricing cut-over per ADR-061 §\"Out of
    scope\". Every role may read (gated by `org.view`).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SeatUsageResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
