from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.feature_flag_kill_response import FeatureFlagKillResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    key: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/flags/{key}/kill".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            key=quote(str(key), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FeatureFlagKillResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FeatureFlagKillResponse.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FeatureFlagKillResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FeatureFlagKillResponse | Problem]:
    """Kill one flag on the latest configuration.

     Requires deploy-write scope and MFA. Sets the flag's kill on the latest published configuration, so
    every evaluation serves the application's fallback. Takes no expected version, re-reads the
    configuration when another publish wins the race, and is idempotent; changed is false when the flag
    was already killed. Unlike other flag writes, an agent connection needs no person's approval for it.
    Revive a flag by publishing the configuration.

    Args:
        slug (str):
        environment (str):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FeatureFlagKillResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
) -> FeatureFlagKillResponse | Problem | None:
    """Kill one flag on the latest configuration.

     Requires deploy-write scope and MFA. Sets the flag's kill on the latest published configuration, so
    every evaluation serves the application's fallback. Takes no expected version, re-reads the
    configuration when another publish wins the race, and is idempotent; changed is false when the flag
    was already killed. Unlike other flag writes, an agent connection needs no person's approval for it.
    Revive a flag by publishing the configuration.

    Args:
        slug (str):
        environment (str):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FeatureFlagKillResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        key=key,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FeatureFlagKillResponse | Problem]:
    """Kill one flag on the latest configuration.

     Requires deploy-write scope and MFA. Sets the flag's kill on the latest published configuration, so
    every evaluation serves the application's fallback. Takes no expected version, re-reads the
    configuration when another publish wins the race, and is idempotent; changed is false when the flag
    was already killed. Unlike other flag writes, an agent connection needs no person's approval for it.
    Revive a flag by publishing the configuration.

    Args:
        slug (str):
        environment (str):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FeatureFlagKillResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
) -> FeatureFlagKillResponse | Problem | None:
    """Kill one flag on the latest configuration.

     Requires deploy-write scope and MFA. Sets the flag's kill on the latest published configuration, so
    every evaluation serves the application's fallback. Takes no expected version, re-reads the
    configuration when another publish wins the race, and is idempotent; changed is false when the flag
    was already killed. Unlike other flag writes, an agent connection needs no person's approval for it.
    Revive a flag by publishing the configuration.

    Args:
        slug (str):
        environment (str):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FeatureFlagKillResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            key=key,
            client=client,
        )
    ).parsed
