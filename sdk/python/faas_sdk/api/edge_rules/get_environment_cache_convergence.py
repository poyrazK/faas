from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_cache_convergence import EnvironmentCacheConvergence
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    environment: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/cache-policy/convergence".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCacheConvergence | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCacheConvergence.from_dict(response.json())

        return response_200

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
) -> Response[EnvironmentCacheConvergence | Problem]:
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
    environment: str,
) -> Response[EnvironmentCacheConvergence | Problem]:
    """Read current-session gateway evidence for an environment cache policy.

     Counts registered active compute gateways with a nonempty serving URL.
    Converged requires every gateway's current session to have loaded the
    exact desired revision, with session and node heartbeats fresh within
    90 seconds. Empty or stale rosters are degraded, never successful.
    Process restart or endpoint replacement invalidates previous-session
    observations. Counts describe sampled evidence, not continuous serving
    behavior or cache savings. Inherited revision zero is degraded because
    no independent environment cache revision exists to acknowledge.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheConvergence | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
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
    environment: str,
) -> EnvironmentCacheConvergence | Problem | None:
    """Read current-session gateway evidence for an environment cache policy.

     Counts registered active compute gateways with a nonempty serving URL.
    Converged requires every gateway's current session to have loaded the
    exact desired revision, with session and node heartbeats fresh within
    90 seconds. Empty or stale rosters are degraded, never successful.
    Process restart or endpoint replacement invalidates previous-session
    observations. Counts describe sampled evidence, not continuous serving
    behavior or cache savings. Inherited revision zero is degraded because
    no independent environment cache revision exists to acknowledge.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheConvergence | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[EnvironmentCacheConvergence | Problem]:
    """Read current-session gateway evidence for an environment cache policy.

     Counts registered active compute gateways with a nonempty serving URL.
    Converged requires every gateway's current session to have loaded the
    exact desired revision, with session and node heartbeats fresh within
    90 seconds. Empty or stale rosters are degraded, never successful.
    Process restart or endpoint replacement invalidates previous-session
    observations. Counts describe sampled evidence, not continuous serving
    behavior or cache savings. Inherited revision zero is degraded because
    no independent environment cache revision exists to acknowledge.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheConvergence | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> EnvironmentCacheConvergence | Problem | None:
    """Read current-session gateway evidence for an environment cache policy.

     Counts registered active compute gateways with a nonempty serving URL.
    Converged requires every gateway's current session to have loaded the
    exact desired revision, with session and node heartbeats fresh within
    90 seconds. Empty or stale rosters are degraded, never successful.
    Process restart or endpoint replacement invalidates previous-session
    observations. Counts describe sampled evidence, not continuous serving
    behavior or cache savings. Inherited revision zero is degraded because
    no independent environment cache revision exists to acknowledge.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheConvergence | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            environment=environment,
        )
    ).parsed
