from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.held_links_response import HeldLinksResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    since: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/held-links",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> HeldLinksResponse | Problem | None:
    if response.status_code == 200:
        response_200 = HeldLinksResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[HeldLinksResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[HeldLinksResponse | Problem]:
    """New accounts linked to abuse-held accounts (operator-only).

     Links detected in the last `since`, newest
    first, with the new account's sign-up country and email domain.
    Defaults to the last 24 hours and 200 rows.

    Args:
        since (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[HeldLinksResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> HeldLinksResponse | Problem | None:
    """New accounts linked to abuse-held accounts (operator-only).

     Links detected in the last `since`, newest
    first, with the new account's sign-up country and email domain.
    Defaults to the last 24 hours and 200 rows.

    Args:
        since (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        HeldLinksResponse | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[HeldLinksResponse | Problem]:
    """New accounts linked to abuse-held accounts (operator-only).

     Links detected in the last `since`, newest
    first, with the new account's sign-up country and email domain.
    Defaults to the last 24 hours and 200 rows.

    Args:
        since (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[HeldLinksResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> HeldLinksResponse | Problem | None:
    """New accounts linked to abuse-held accounts (operator-only).

     Links detected in the last `since`, newest
    first, with the new account's sign-up country and email domain.
    Defaults to the last 24 hours and 200 rows.

    Args:
        since (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        HeldLinksResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
            limit=limit,
        )
    ).parsed
