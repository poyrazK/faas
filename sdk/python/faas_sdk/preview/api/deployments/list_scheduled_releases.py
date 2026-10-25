from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.scheduled_release_list_response import ScheduledReleaseListResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["app"] = app

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/scheduled-releases",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScheduledReleaseListResponse | None:
    if response.status_code == 200:
        response_200 = ScheduledReleaseListResponse.from_dict(response.json())

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScheduledReleaseListResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> Response[Problem | ScheduledReleaseListResponse]:
    """List scheduled releases (ADR-20261021152201).

     The account's scheduled releases: pending ones first, soonest first, then the most recently ended.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScheduledReleaseListResponse]
    """

    kwargs = _get_kwargs(
        app=app,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> Problem | ScheduledReleaseListResponse | None:
    """List scheduled releases (ADR-20261021152201).

     The account's scheduled releases: pending ones first, soonest first, then the most recently ended.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScheduledReleaseListResponse
    """

    return sync_detailed(
        client=client,
        app=app,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> Response[Problem | ScheduledReleaseListResponse]:
    """List scheduled releases (ADR-20261021152201).

     The account's scheduled releases: pending ones first, soonest first, then the most recently ended.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScheduledReleaseListResponse]
    """

    kwargs = _get_kwargs(
        app=app,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> Problem | ScheduledReleaseListResponse | None:
    """List scheduled releases (ADR-20261021152201).

     The account's scheduled releases: pending ones first, soonest first, then the most recently ended.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScheduledReleaseListResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            app=app,
        )
    ).parsed
