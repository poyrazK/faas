from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_release_freeze_request import CreateReleaseFreezeRequest
from ...models.problem import Problem
from ...models.release_freeze import ReleaseFreeze
from ...types import Response


def _get_kwargs(
    *,
    body: CreateReleaseFreezeRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/freezes",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ReleaseFreeze | None:
    if response.status_code == 201:
        response_201 = ReleaseFreeze.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ReleaseFreeze]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateReleaseFreezeRequest,
) -> Response[Problem | ReleaseFreeze]:
    """Freeze production releases.

     Creates a window, weekly or after-rollback freeze for one app or, without `app`, for every app of
    the account. While a freeze is active, new production deployments are refused with `release_frozen`
    unless the request carries `override_freeze`.

    Args:
        body (CreateReleaseFreezeRequest): Creates a freeze. A window takes `starts_at` (default
            now) and optional `ends_at`; a weekly freeze takes `weekly`; an after-rollback policy
            takes `after_rollback_minutes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseFreeze]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateReleaseFreezeRequest,
) -> Problem | ReleaseFreeze | None:
    """Freeze production releases.

     Creates a window, weekly or after-rollback freeze for one app or, without `app`, for every app of
    the account. While a freeze is active, new production deployments are refused with `release_frozen`
    unless the request carries `override_freeze`.

    Args:
        body (CreateReleaseFreezeRequest): Creates a freeze. A window takes `starts_at` (default
            now) and optional `ends_at`; a weekly freeze takes `weekly`; an after-rollback policy
            takes `after_rollback_minutes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseFreeze
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateReleaseFreezeRequest,
) -> Response[Problem | ReleaseFreeze]:
    """Freeze production releases.

     Creates a window, weekly or after-rollback freeze for one app or, without `app`, for every app of
    the account. While a freeze is active, new production deployments are refused with `release_frozen`
    unless the request carries `override_freeze`.

    Args:
        body (CreateReleaseFreezeRequest): Creates a freeze. A window takes `starts_at` (default
            now) and optional `ends_at`; a weekly freeze takes `weekly`; an after-rollback policy
            takes `after_rollback_minutes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseFreeze]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateReleaseFreezeRequest,
) -> Problem | ReleaseFreeze | None:
    """Freeze production releases.

     Creates a window, weekly or after-rollback freeze for one app or, without `app`, for every app of
    the account. While a freeze is active, new production deployments are refused with `release_frozen`
    unless the request carries `override_freeze`.

    Args:
        body (CreateReleaseFreezeRequest): Creates a freeze. A window takes `starts_at` (default
            now) and optional `ends_at`; a weekly freeze takes `weekly`; an after-rollback policy
            takes `after_rollback_minutes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseFreeze
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
