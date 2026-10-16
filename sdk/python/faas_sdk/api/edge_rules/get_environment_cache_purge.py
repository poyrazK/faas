from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_cache_purge_status import EnvironmentCachePurgeStatus
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    request: UUID,
    *,
    environment: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/cache/purges/{request}".format(
            slug=quote(str(slug), safe=""),
            request=quote(str(request), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCachePurgeStatus | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCachePurgeStatus.from_dict(response.json())

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
) -> Response[EnvironmentCachePurgeStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[EnvironmentCachePurgeStatus | Problem]:
    """Read a scoped cache purge receipt and gateway completion evidence.

     Requires app read access and MFA. Returns completed only when every
    registered active serving compute gateway has replayed this generation
    in its current process session with fresh node and session heartbeats
    within 90 seconds. Fresh sessions without the checkpoint are pending;
    missing/stale sessions, changed endpoints and empty fleets are degraded.
    Reads identity, receipt and aggregate counts from one snapshot and
    rechecks the environment registry. Recreated environments cannot read
    old receipts. No gateway names, addresses or unrelated cache data are
    returned. Read-only; a later restart or roster change may change status.

    Args:
        slug (str):
        request (UUID):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePurgeStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request=request,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> EnvironmentCachePurgeStatus | Problem | None:
    """Read a scoped cache purge receipt and gateway completion evidence.

     Requires app read access and MFA. Returns completed only when every
    registered active serving compute gateway has replayed this generation
    in its current process session with fresh node and session heartbeats
    within 90 seconds. Fresh sessions without the checkpoint are pending;
    missing/stale sessions, changed endpoints and empty fleets are degraded.
    Reads identity, receipt and aggregate counts from one snapshot and
    rechecks the environment registry. Recreated environments cannot read
    old receipts. No gateway names, addresses or unrelated cache data are
    returned. Read-only; a later restart or roster change may change status.

    Args:
        slug (str):
        request (UUID):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePurgeStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        request=request,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> Response[EnvironmentCachePurgeStatus | Problem]:
    """Read a scoped cache purge receipt and gateway completion evidence.

     Requires app read access and MFA. Returns completed only when every
    registered active serving compute gateway has replayed this generation
    in its current process session with fresh node and session heartbeats
    within 90 seconds. Fresh sessions without the checkpoint are pending;
    missing/stale sessions, changed endpoints and empty fleets are degraded.
    Reads identity, receipt and aggregate counts from one snapshot and
    rechecks the environment registry. Recreated environments cannot read
    old receipts. No gateway names, addresses or unrelated cache data are
    returned. Read-only; a later restart or roster change may change status.

    Args:
        slug (str):
        request (UUID):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePurgeStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request=request,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str,
) -> EnvironmentCachePurgeStatus | Problem | None:
    """Read a scoped cache purge receipt and gateway completion evidence.

     Requires app read access and MFA. Returns completed only when every
    registered active serving compute gateway has replayed this generation
    in its current process session with fresh node and session heartbeats
    within 90 seconds. Fresh sessions without the checkpoint are pending;
    missing/stale sessions, changed endpoints and empty fleets are degraded.
    Reads identity, receipt and aggregate counts from one snapshot and
    rechecks the environment registry. Recreated environments cannot read
    old receipts. No gateway names, addresses or unrelated cache data are
    returned. Read-only; a later restart or roster change may change status.

    Args:
        slug (str):
        request (UUID):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePurgeStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            request=request,
            client=client,
            environment=environment,
        )
    ).parsed
