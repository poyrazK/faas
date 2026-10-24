from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_error_requests_response import AppErrorRequestsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    fingerprint: str,
    *,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_cursor: None | str | Unset
    if isinstance(cursor, Unset):
        json_cursor = UNSET
    else:
        json_cursor = cursor
    params["cursor"] = json_cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/errors/{fingerprint}".format(
            slug=quote(str(slug), safe=""),
            fingerprint=quote(str(fingerprint), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppErrorRequestsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppErrorRequestsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppErrorRequestsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[AppErrorRequestsResponse | Problem]:
    r"""Per-fingerprint drill-down rows (ADR-096 / PR-B).

     Cursor-paginated drill-down over the request rows that
    landed on this fingerprint. Returns 404 when the
    fingerprint has been purged by the retention cron or
    never existed; the cross-account slug case is also 404
    (IDOR-safe byte-identical to a real \"no such app\" 404).

    Args:
        slug (str):
        fingerprint (str):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppErrorRequestsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        fingerprint=fingerprint,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> AppErrorRequestsResponse | Problem | None:
    r"""Per-fingerprint drill-down rows (ADR-096 / PR-B).

     Cursor-paginated drill-down over the request rows that
    landed on this fingerprint. Returns 404 when the
    fingerprint has been purged by the retention cron or
    never existed; the cross-account slug case is also 404
    (IDOR-safe byte-identical to a real \"no such app\" 404).

    Args:
        slug (str):
        fingerprint (str):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppErrorRequestsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        fingerprint=fingerprint,
        client=client,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[AppErrorRequestsResponse | Problem]:
    r"""Per-fingerprint drill-down rows (ADR-096 / PR-B).

     Cursor-paginated drill-down over the request rows that
    landed on this fingerprint. Returns 404 when the
    fingerprint has been purged by the retention cron or
    never existed; the cross-account slug case is also 404
    (IDOR-safe byte-identical to a real \"no such app\" 404).

    Args:
        slug (str):
        fingerprint (str):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppErrorRequestsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        fingerprint=fingerprint,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
    cursor: None | str | Unset = UNSET,
    limit: int | Unset = 20,
) -> AppErrorRequestsResponse | Problem | None:
    r"""Per-fingerprint drill-down rows (ADR-096 / PR-B).

     Cursor-paginated drill-down over the request rows that
    landed on this fingerprint. Returns 404 when the
    fingerprint has been purged by the retention cron or
    never existed; the cross-account slug case is also 404
    (IDOR-safe byte-identical to a real \"no such app\" 404).

    Args:
        slug (str):
        fingerprint (str):
        cursor (None | str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppErrorRequestsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            fingerprint=fingerprint,
            client=client,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
