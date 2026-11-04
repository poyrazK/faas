from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_route_budgets import EnvironmentRouteBudgets
from ...models.get_environment_route_budgets_scope import (
    GetEnvironmentRouteBudgetsScope,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: GetEnvironmentRouteBudgetsScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_scope: str | Unset = UNSET
    if not isinstance(scope, Unset):
        json_scope = scope

    params["scope"] = json_scope

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-budgets".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentRouteBudgets | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentRouteBudgets.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EnvironmentRouteBudgets | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetsScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> Response[EnvironmentRouteBudgets | Problem]:
    """Read effective advisory budgets for one environment.

     Requires app read access and completed MFA. Defaults to app-budget inheritance. Reports immutable
    identity, override revision and parent revision. Excludes customer grouping and recovery actions.

    Args:
        slug (str):
        scope (GetEnvironmentRouteBudgetsScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentRouteBudgets | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetsScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> EnvironmentRouteBudgets | Problem | None:
    """Read effective advisory budgets for one environment.

     Requires app read access and completed MFA. Defaults to app-budget inheritance. Reports immutable
    identity, override revision and parent revision. Excludes customer grouping and recovery actions.

    Args:
        slug (str):
        scope (GetEnvironmentRouteBudgetsScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentRouteBudgets | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetsScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> Response[EnvironmentRouteBudgets | Problem]:
    """Read effective advisory budgets for one environment.

     Requires app read access and completed MFA. Defaults to app-budget inheritance. Reports immutable
    identity, override revision and parent revision. Excludes customer grouping and recovery actions.

    Args:
        slug (str):
        scope (GetEnvironmentRouteBudgetsScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentRouteBudgets | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: GetEnvironmentRouteBudgetsScope | Unset = UNSET,
    environment: str | Unset = UNSET,
) -> EnvironmentRouteBudgets | Problem | None:
    """Read effective advisory budgets for one environment.

     Requires app read access and completed MFA. Defaults to app-budget inheritance. Reports immutable
    identity, override revision and parent revision. Excludes customer grouping and recovery actions.

    Args:
        slug (str):
        scope (GetEnvironmentRouteBudgetsScope | Unset):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentRouteBudgets | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            environment=environment,
        )
    ).parsed
