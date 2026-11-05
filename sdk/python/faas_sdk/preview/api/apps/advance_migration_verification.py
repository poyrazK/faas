from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.migration_verification import MigrationVerification
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/migration-verifications/{run}/advance".format(
            slug=quote(str(slug), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MigrationVerification | Problem | None:
    if response.status_code == 200:
        response_200 = MigrationVerification.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MigrationVerification | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[MigrationVerification | Problem]:
    """Advance one saved migration verification step.

     Advance one leased step of an existing migration receipt. Dependency
    tasks and their IDs commit atomically. Startup and post-wake HTTP checks
    require a successful exact-deployment gateway response proof. Cold wake
    waits for zero active instances and a running instance with the saved
    wake identity. Replays preserve task IDs and wake correlation. The
    server worker continues pending runs independently of clients and
    resumes after restart. This operation is an optional compatibility
    action; clients normally poll GET. Cancelled, expired or invalidated
    runs stop tasks and restore a service they may have parked before
    becoming terminal. Recovery retries continue beyond the checking deadline.
    Requires deployment write scope, verified email and MFA. Changed inputs
    block qualification; infrastructure errors preserve resumable progress.

    Args:
        slug (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationVerification | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        run=run,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> MigrationVerification | Problem | None:
    """Advance one saved migration verification step.

     Advance one leased step of an existing migration receipt. Dependency
    tasks and their IDs commit atomically. Startup and post-wake HTTP checks
    require a successful exact-deployment gateway response proof. Cold wake
    waits for zero active instances and a running instance with the saved
    wake identity. Replays preserve task IDs and wake correlation. The
    server worker continues pending runs independently of clients and
    resumes after restart. This operation is an optional compatibility
    action; clients normally poll GET. Cancelled, expired or invalidated
    runs stop tasks and restore a service they may have parked before
    becoming terminal. Recovery retries continue beyond the checking deadline.
    Requires deployment write scope, verified email and MFA. Changed inputs
    block qualification; infrastructure errors preserve resumable progress.

    Args:
        slug (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationVerification | Problem
    """

    return sync_detailed(
        slug=slug,
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[MigrationVerification | Problem]:
    """Advance one saved migration verification step.

     Advance one leased step of an existing migration receipt. Dependency
    tasks and their IDs commit atomically. Startup and post-wake HTTP checks
    require a successful exact-deployment gateway response proof. Cold wake
    waits for zero active instances and a running instance with the saved
    wake identity. Replays preserve task IDs and wake correlation. The
    server worker continues pending runs independently of clients and
    resumes after restart. This operation is an optional compatibility
    action; clients normally poll GET. Cancelled, expired or invalidated
    runs stop tasks and restore a service they may have parked before
    becoming terminal. Recovery retries continue beyond the checking deadline.
    Requires deployment write scope, verified email and MFA. Changed inputs
    block qualification; infrastructure errors preserve resumable progress.

    Args:
        slug (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationVerification | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        run=run,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> MigrationVerification | Problem | None:
    """Advance one saved migration verification step.

     Advance one leased step of an existing migration receipt. Dependency
    tasks and their IDs commit atomically. Startup and post-wake HTTP checks
    require a successful exact-deployment gateway response proof. Cold wake
    waits for zero active instances and a running instance with the saved
    wake identity. Replays preserve task IDs and wake correlation. The
    server worker continues pending runs independently of clients and
    resumes after restart. This operation is an optional compatibility
    action; clients normally poll GET. Cancelled, expired or invalidated
    runs stop tasks and restore a service they may have parked before
    becoming terminal. Recovery retries continue beyond the checking deadline.
    Requires deployment write scope, verified email and MFA. Changed inputs
    block qualification; infrastructure errors preserve resumable progress.

    Args:
        slug (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationVerification | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            run=run,
            client=client,
        )
    ).parsed
