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
        "url": "/v1/apps/{slug}/migration-verifications/{run}/cancel".format(
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
    """Cancel saved migration checks and recover a parked service.

     Persist idempotent cancellation intent. An active worker lease remains
    fenced until its bounded step ends or the lease expires. No further
    checks can commit after cancellation. Background cleanup cancels saved
    tasks and restores the same deployment if this run may have parked it.
    Status remains pending while cleanup runs; recovery_status describes
    restoration, supersession by another deployment or an account/security
    block. A wake notification alone never means service was restored.
    Completed runs are unchanged. Requires deployment write scope, verified
    email and MFA; remains available when task admission is disabled.

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
    """Cancel saved migration checks and recover a parked service.

     Persist idempotent cancellation intent. An active worker lease remains
    fenced until its bounded step ends or the lease expires. No further
    checks can commit after cancellation. Background cleanup cancels saved
    tasks and restores the same deployment if this run may have parked it.
    Status remains pending while cleanup runs; recovery_status describes
    restoration, supersession by another deployment or an account/security
    block. A wake notification alone never means service was restored.
    Completed runs are unchanged. Requires deployment write scope, verified
    email and MFA; remains available when task admission is disabled.

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
    """Cancel saved migration checks and recover a parked service.

     Persist idempotent cancellation intent. An active worker lease remains
    fenced until its bounded step ends or the lease expires. No further
    checks can commit after cancellation. Background cleanup cancels saved
    tasks and restores the same deployment if this run may have parked it.
    Status remains pending while cleanup runs; recovery_status describes
    restoration, supersession by another deployment or an account/security
    block. A wake notification alone never means service was restored.
    Completed runs are unchanged. Requires deployment write scope, verified
    email and MFA; remains available when task admission is disabled.

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
    """Cancel saved migration checks and recover a parked service.

     Persist idempotent cancellation intent. An active worker lease remains
    fenced until its bounded step ends or the lease expires. No further
    checks can commit after cancellation. Background cleanup cancels saved
    tasks and restores the same deployment if this run may have parked it.
    Status remains pending while cleanup runs; recovery_status describes
    restoration, supersession by another deployment or an account/security
    block. A wake notification alone never means service was restored.
    Completed runs are unchanged. Requires deployment write scope, verified
    email and MFA; remains available when task admission is disabled.

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
