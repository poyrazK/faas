from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_cache_policy import EnvironmentCachePolicy
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    revision: int,
    *,
    environment: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/cache-policy/history/{revision}".format(
            slug=quote(str(slug), safe=""),
            revision=quote(str(revision), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCachePolicy | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCachePolicy.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EnvironmentCachePolicy | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[EnvironmentCachePolicy | Problem]:
    """Read one retained environment cache policy revision.

     Requires app read access and MFA. Returns 404 for unavailable positive revisions. Revision zero is
    synthetic initial live inheritance, not historical app rule bodies. Identity and history are read
    from one snapshot; environment replacement cannot recover old snapshots.

    Args:
        slug (str):
        revision (int):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        revision=revision,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> EnvironmentCachePolicy | Problem | None:
    """Read one retained environment cache policy revision.

     Requires app read access and MFA. Returns 404 for unavailable positive revisions. Revision zero is
    synthetic initial live inheritance, not historical app rule bodies. Identity and history are read
    from one snapshot; environment replacement cannot recover old snapshots.

    Args:
        slug (str):
        revision (int):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePolicy | Problem
    """

    return sync_detailed(
        slug=slug,
        revision=revision,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[EnvironmentCachePolicy | Problem]:
    """Read one retained environment cache policy revision.

     Requires app read access and MFA. Returns 404 for unavailable positive revisions. Revision zero is
    synthetic initial live inheritance, not historical app rule bodies. Identity and history are read
    from one snapshot; environment replacement cannot recover old snapshots.

    Args:
        slug (str):
        revision (int):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        revision=revision,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> EnvironmentCachePolicy | Problem | None:
    """Read one retained environment cache policy revision.

     Requires app read access and MFA. Returns 404 for unavailable positive revisions. Revision zero is
    synthetic initial live inheritance, not historical app rule bodies. Identity and history are read
    from one snapshot; environment replacement cannot recover old snapshots.

    Args:
        slug (str):
        revision (int):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            revision=revision,
            client=client,
            environment=environment,
        )
    ).parsed
