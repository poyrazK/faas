from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_migration_cutover_request import CreateMigrationCutoverRequest
from ...models.migration_cutover import MigrationCutover
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateMigrationCutoverRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/migration-cutovers".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    *,
    client: AuthenticatedClient,
    body: CreateMigrationCutoverRequest,
) -> Response[MigrationCutover | Problem]:
    """Prepare a production switch without changing traffic.

     Save a plan for a currently passed default-scope verification. Supply a stable
    caller UUID, production hostname, previous routing records and TTLs, previous
    proxy setting and traffic budgets. Reusing an ID returns its original intent;
    changed intent conflicts. Preparation edits no DNS, domain binding, rollout,
    default hostname or monitor policy. Connect the domain through the existing
    domain API before starting checks. Requires deployment write scope, verified
    email and MFA.

    Args:
        slug (str):
        body (CreateMigrationCutoverRequest): Immutable production cutover plan. The stable ID
            reconciles lost responses. Preparation changes no traffic or domain bindings.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationCutover | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateMigrationCutoverRequest,
) -> MigrationCutover | Problem | None:
    """Prepare a production switch without changing traffic.

     Save a plan for a currently passed default-scope verification. Supply a stable
    caller UUID, production hostname, previous routing records and TTLs, previous
    proxy setting and traffic budgets. Reusing an ID returns its original intent;
    changed intent conflicts. Preparation edits no DNS, domain binding, rollout,
    default hostname or monitor policy. Connect the domain through the existing
    domain API before starting checks. Requires deployment write scope, verified
    email and MFA.

    Args:
        slug (str):
        body (CreateMigrationCutoverRequest): Immutable production cutover plan. The stable ID
            reconciles lost responses. Preparation changes no traffic or domain bindings.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationCutover | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateMigrationCutoverRequest,
) -> Response[MigrationCutover | Problem]:
    """Prepare a production switch without changing traffic.

     Save a plan for a currently passed default-scope verification. Supply a stable
    caller UUID, production hostname, previous routing records and TTLs, previous
    proxy setting and traffic budgets. Reusing an ID returns its original intent;
    changed intent conflicts. Preparation edits no DNS, domain binding, rollout,
    default hostname or monitor policy. Connect the domain through the existing
    domain API before starting checks. Requires deployment write scope, verified
    email and MFA.

    Args:
        slug (str):
        body (CreateMigrationCutoverRequest): Immutable production cutover plan. The stable ID
            reconciles lost responses. Preparation changes no traffic or domain bindings.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationCutover | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: CreateMigrationCutoverRequest,
) -> MigrationCutover | Problem | None:
    """Prepare a production switch without changing traffic.

     Save a plan for a currently passed default-scope verification. Supply a stable
    caller UUID, production hostname, previous routing records and TTLs, previous
    proxy setting and traffic budgets. Reusing an ID returns its original intent;
    changed intent conflicts. Preparation edits no DNS, domain binding, rollout,
    default hostname or monitor policy. Connect the domain through the existing
    domain API before starting checks. Requires deployment write scope, verified
    email and MFA.

    Args:
        slug (str):
        body (CreateMigrationCutoverRequest): Immutable production cutover plan. The stable ID
            reconciles lost responses. Preparation changes no traffic or domain bindings.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationCutover | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
