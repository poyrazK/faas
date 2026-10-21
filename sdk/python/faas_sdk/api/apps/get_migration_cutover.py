from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.migration_cutover import MigrationCutover
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    plan: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/migration-cutovers/{plan}".format(
            slug=quote(str(slug), safe=""),
            plan=quote(str(plan), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MigrationCutover | Problem | None:
    if response.status_code == 200:
        response_200 = MigrationCutover.from_dict(response.json())

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
) -> Response[MigrationCutover | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[MigrationCutover | Problem]:
    """Read a saved production cutover plan.

     Read one saved production switch and its original DNS recovery settings.
    Changed source verification, deployment configuration or domain binding blocks
    its evidence. Reads perform no HTTP or DNS probes. Requires app read scope
    and MFA; a blocked plan retains its previous-provider routing instructions.

    Args:
        slug (str):
        plan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationCutover | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        plan=plan,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
) -> MigrationCutover | Problem | None:
    """Read a saved production cutover plan.

     Read one saved production switch and its original DNS recovery settings.
    Changed source verification, deployment configuration or domain binding blocks
    its evidence. Reads perform no HTTP or DNS probes. Requires app read scope
    and MFA; a blocked plan retains its previous-provider routing instructions.

    Args:
        slug (str):
        plan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationCutover | Problem
    """

    return sync_detailed(
        slug=slug,
        plan=plan,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[MigrationCutover | Problem]:
    """Read a saved production cutover plan.

     Read one saved production switch and its original DNS recovery settings.
    Changed source verification, deployment configuration or domain binding blocks
    its evidence. Reads perform no HTTP or DNS probes. Requires app read scope
    and MFA; a blocked plan retains its previous-provider routing instructions.

    Args:
        slug (str):
        plan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationCutover | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        plan=plan,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
) -> MigrationCutover | Problem | None:
    """Read a saved production cutover plan.

     Read one saved production switch and its original DNS recovery settings.
    Changed source verification, deployment configuration or domain binding blocks
    its evidence. Reads perform no HTTP or DNS probes. Requires app read scope
    and MFA; a blocked plan retains its previous-provider routing instructions.

    Args:
        slug (str):
        plan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationCutover | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            plan=plan,
            client=client,
        )
    ).parsed
