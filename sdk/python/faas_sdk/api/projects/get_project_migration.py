from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_migration import ProjectMigration
from ...types import Response


def _get_kwargs(
    migration: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/project-migrations/{migration}".format(
            migration=quote(str(migration), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ProjectMigration:
    if response.status_code == 200:
        response_200 = ProjectMigration.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectMigration]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    migration: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | ProjectMigration]:
    """Inspect each reserved deployment and its recorded verification and cutover

     Inspect each reserved deployment and its recorded verification and cutover. Source-backed HTTP and
    GraphQL services only. Secret values are request-only; the receipt retains key names. Existing
    reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No
    domain or production traffic changes are performed by this endpoint.

    Args:
        migration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectMigration]
    """

    kwargs = _get_kwargs(
        migration=migration,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    migration: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | ProjectMigration | None:
    """Inspect each reserved deployment and its recorded verification and cutover

     Inspect each reserved deployment and its recorded verification and cutover. Source-backed HTTP and
    GraphQL services only. Secret values are request-only; the receipt retains key names. Existing
    reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No
    domain or production traffic changes are performed by this endpoint.

    Args:
        migration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectMigration
    """

    return sync_detailed(
        migration=migration,
        client=client,
    ).parsed


async def asyncio_detailed(
    migration: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | ProjectMigration]:
    """Inspect each reserved deployment and its recorded verification and cutover

     Inspect each reserved deployment and its recorded verification and cutover. Source-backed HTTP and
    GraphQL services only. Secret values are request-only; the receipt retains key names. Existing
    reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No
    domain or production traffic changes are performed by this endpoint.

    Args:
        migration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectMigration]
    """

    kwargs = _get_kwargs(
        migration=migration,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    migration: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | ProjectMigration | None:
    """Inspect each reserved deployment and its recorded verification and cutover

     Inspect each reserved deployment and its recorded verification and cutover. Source-backed HTTP and
    GraphQL services only. Secret values are request-only; the receipt retains key names. Existing
    reconcile, sealing, deployment admission, verification and cutover owners remain authoritative. No
    domain or production traffic changes are performed by this endpoint.

    Args:
        migration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectMigration
    """

    return (
        await asyncio_detailed(
            migration=migration,
            client=client,
        )
    ).parsed
