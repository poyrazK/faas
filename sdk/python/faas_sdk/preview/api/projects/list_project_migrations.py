from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_migration_list import ProjectMigrationList
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    project: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["project"] = project

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/project-migrations",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectMigrationList:
    if response.status_code == 200:
        response_200 = ProjectMigrationList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectMigrationList]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    project: str | Unset = UNSET,
) -> Response[Problem | ProjectMigrationList]:
    """List saved project migrations for the authenticated account

     List saved project migrations for the authenticated account. Source-backed HTTP and GraphQL services
    only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        project (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectMigrationList]
    """

    kwargs = _get_kwargs(
        project=project,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    project: str | Unset = UNSET,
) -> Problem | ProjectMigrationList | None:
    """List saved project migrations for the authenticated account

     List saved project migrations for the authenticated account. Source-backed HTTP and GraphQL services
    only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        project (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectMigrationList
    """

    return sync_detailed(
        client=client,
        project=project,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    project: str | Unset = UNSET,
) -> Response[Problem | ProjectMigrationList]:
    """List saved project migrations for the authenticated account

     List saved project migrations for the authenticated account. Source-backed HTTP and GraphQL services
    only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        project (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectMigrationList]
    """

    kwargs = _get_kwargs(
        project=project,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    project: str | Unset = UNSET,
) -> Problem | ProjectMigrationList | None:
    """List saved project migrations for the authenticated account

     List saved project migrations for the authenticated account. Source-backed HTTP and GraphQL services
    only. Secret values are request-only; the receipt retains key names. Existing reconcile, sealing,
    deployment admission, verification and cutover owners remain authoritative. No domain or production
    traffic changes are performed by this endpoint.

    Args:
        project (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectMigrationList
    """

    return (
        await asyncio_detailed(
            client=client,
            project=project,
        )
    ).parsed
