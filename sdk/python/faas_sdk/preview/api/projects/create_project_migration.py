from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_project_migration_request import CreateProjectMigrationRequest
from ...models.problem import Problem
from ...models.project_migration import ProjectMigration
from ...types import Response


def _get_kwargs(
    *,
    body: CreateProjectMigrationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/project-migrations",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    *,
    client: AuthenticatedClient,
    body: CreateProjectMigrationRequest,
) -> Response[Problem | ProjectMigration]:
    """Save and apply a reviewed multi-service migration

     Save and apply a reviewed multi-service migration. Source-backed HTTP and GraphQL services only.
    Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        body (CreateProjectMigrationRequest): Start one durable migration of selected reviewed
            services with request-only configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectMigration]
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
    client: AuthenticatedClient,
    body: CreateProjectMigrationRequest,
) -> Problem | ProjectMigration | None:
    """Save and apply a reviewed multi-service migration

     Save and apply a reviewed multi-service migration. Source-backed HTTP and GraphQL services only.
    Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        body (CreateProjectMigrationRequest): Start one durable migration of selected reviewed
            services with request-only configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectMigration
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateProjectMigrationRequest,
) -> Response[Problem | ProjectMigration]:
    """Save and apply a reviewed multi-service migration

     Save and apply a reviewed multi-service migration. Source-backed HTTP and GraphQL services only.
    Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        body (CreateProjectMigrationRequest): Start one durable migration of selected reviewed
            services with request-only configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectMigration]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateProjectMigrationRequest,
) -> Problem | ProjectMigration | None:
    """Save and apply a reviewed multi-service migration

     Save and apply a reviewed multi-service migration. Source-backed HTTP and GraphQL services only.
    Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        body (CreateProjectMigrationRequest): Start one durable migration of selected reviewed
            services with request-only configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectMigration
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
