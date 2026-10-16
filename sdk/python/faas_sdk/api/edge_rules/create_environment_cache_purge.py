from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_environment_cache_purge_request import CreateEnvironmentCachePurgeRequest
from ...models.environment_cache_purge_receipt import EnvironmentCachePurgeReceipt
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateEnvironmentCachePurgeRequest,
    environment: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/cache/purges".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCachePurgeReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCachePurgeReceipt.from_dict(response.json())

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
) -> Response[EnvironmentCachePurgeReceipt | Problem]:
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
    body: CreateEnvironmentCachePurgeRequest,
    environment: str,
) -> Response[EnvironmentCachePurgeReceipt | Problem]:
    """Request an exact-path or whole-environment logical cache purge.

     Requires app write access and MFA. Immutable app/project/environment IDs
    and request_id are required. Path must be explicitly supplied: empty
    string targets the entire immutable environment host, otherwise an exact
    decoded URL path without query, fragment, percent escapes, wildcards,
    controls or dot segments. All query, method, rule and vary variants at
    that path are invalidated; other environment hosts and policy are unchanged.
    A commit-ordered durable generation and retry receipt are the same row.
    Retry the identical request_id and body after a lost reply; changed target
    reuse returns conflict. New gateways bypass environment caching until
    replayed; prior-generation L1/L2 entries and late cache writes become
    unreachable. Redis objects expire by TTL rather than physical deletion.
    The receipt confirms desired intent. Read purge status for current-session
    gateway acknowledgement; completion is sampled registered-fleet evidence,
    not an in-flight response drain or application-data freshness guarantee.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCachePurgeRequest): Immutable named-environment purge target and
            stable retry identity; path must be explicit even for whole-environment purging.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePurgeReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
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
    body: CreateEnvironmentCachePurgeRequest,
    environment: str,
) -> EnvironmentCachePurgeReceipt | Problem | None:
    """Request an exact-path or whole-environment logical cache purge.

     Requires app write access and MFA. Immutable app/project/environment IDs
    and request_id are required. Path must be explicitly supplied: empty
    string targets the entire immutable environment host, otherwise an exact
    decoded URL path without query, fragment, percent escapes, wildcards,
    controls or dot segments. All query, method, rule and vary variants at
    that path are invalidated; other environment hosts and policy are unchanged.
    A commit-ordered durable generation and retry receipt are the same row.
    Retry the identical request_id and body after a lost reply; changed target
    reuse returns conflict. New gateways bypass environment caching until
    replayed; prior-generation L1/L2 entries and late cache writes become
    unreachable. Redis objects expire by TTL rather than physical deletion.
    The receipt confirms desired intent. Read purge status for current-session
    gateway acknowledgement; completion is sampled registered-fleet evidence,
    not an in-flight response drain or application-data freshness guarantee.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCachePurgeRequest): Immutable named-environment purge target and
            stable retry identity; path must be explicit even for whole-environment purging.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePurgeReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEnvironmentCachePurgeRequest,
    environment: str,
) -> Response[EnvironmentCachePurgeReceipt | Problem]:
    """Request an exact-path or whole-environment logical cache purge.

     Requires app write access and MFA. Immutable app/project/environment IDs
    and request_id are required. Path must be explicitly supplied: empty
    string targets the entire immutable environment host, otherwise an exact
    decoded URL path without query, fragment, percent escapes, wildcards,
    controls or dot segments. All query, method, rule and vary variants at
    that path are invalidated; other environment hosts and policy are unchanged.
    A commit-ordered durable generation and retry receipt are the same row.
    Retry the identical request_id and body after a lost reply; changed target
    reuse returns conflict. New gateways bypass environment caching until
    replayed; prior-generation L1/L2 entries and late cache writes become
    unreachable. Redis objects expire by TTL rather than physical deletion.
    The receipt confirms desired intent. Read purge status for current-session
    gateway acknowledgement; completion is sampled registered-fleet evidence,
    not an in-flight response drain or application-data freshness guarantee.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCachePurgeRequest): Immutable named-environment purge target and
            stable retry identity; path must be explicit even for whole-environment purging.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePurgeReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEnvironmentCachePurgeRequest,
    environment: str,
) -> EnvironmentCachePurgeReceipt | Problem | None:
    """Request an exact-path or whole-environment logical cache purge.

     Requires app write access and MFA. Immutable app/project/environment IDs
    and request_id are required. Path must be explicitly supplied: empty
    string targets the entire immutable environment host, otherwise an exact
    decoded URL path without query, fragment, percent escapes, wildcards,
    controls or dot segments. All query, method, rule and vary variants at
    that path are invalidated; other environment hosts and policy are unchanged.
    A commit-ordered durable generation and retry receipt are the same row.
    Retry the identical request_id and body after a lost reply; changed target
    reuse returns conflict. New gateways bypass environment caching until
    replayed; prior-generation L1/L2 entries and late cache writes become
    unreachable. Redis objects expire by TTL rather than physical deletion.
    The receipt confirms desired intent. Read purge status for current-session
    gateway acknowledgement; completion is sampled registered-fleet evidence,
    not an in-flight response drain or application-data freshness guarantee.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCachePurgeRequest): Immutable named-environment purge target and
            stable retry identity; path must be explicit even for whole-environment purging.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePurgeReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            environment=environment,
        )
    ).parsed
