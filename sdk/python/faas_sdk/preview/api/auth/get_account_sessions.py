from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.session_list_response import SessionListResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/auth/sessions",
        "cookies": cookies,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SessionListResponse | None:
    if response.status_code == 200:
        response_200 = SessionListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SessionListResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | SessionListResponse]:
    """List active sessions for the calling account.

     Returns one `SessionInfo` row per active login (one row
    per dashboard login; ADR-039 / IAM-3). Newest first. The
    row whose `id` matches the calling cookie's `sid` is
    flagged `current_session: true`. Revoked rows are NOT
    returned; the `audit-events` endpoint is the timeline
    for those.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SessionListResponse]
    """

    kwargs = _get_kwargs(
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Problem | SessionListResponse | None:
    """List active sessions for the calling account.

     Returns one `SessionInfo` row per active login (one row
    per dashboard login; ADR-039 / IAM-3). Newest first. The
    row whose `id` matches the calling cookie's `sid` is
    flagged `current_session: true`. Revoked rows are NOT
    returned; the `audit-events` endpoint is the timeline
    for those.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SessionListResponse
    """

    return sync_detailed(
        client=client,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | SessionListResponse]:
    """List active sessions for the calling account.

     Returns one `SessionInfo` row per active login (one row
    per dashboard login; ADR-039 / IAM-3). Newest first. The
    row whose `id` matches the calling cookie's `sid` is
    flagged `current_session: true`. Revoked rows are NOT
    returned; the `audit-events` endpoint is the timeline
    for those.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SessionListResponse]
    """

    kwargs = _get_kwargs(
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Problem | SessionListResponse | None:
    """List active sessions for the calling account.

     Returns one `SessionInfo` row per active login (one row
    per dashboard login; ADR-039 / IAM-3). Newest first. The
    row whose `id` matches the calling cookie's `sid` is
    flagged `current_session: true`. Revoked rows are NOT
    returned; the `audit-events` endpoint is the timeline
    for those.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SessionListResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            faas_sid=faas_sid,
        )
    ).parsed
