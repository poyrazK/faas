from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_cache_policy import EnvironmentCachePolicy
from ...models.problem import Problem
from ...models.set_environment_cache_policy_request import SetEnvironmentCachePolicyRequest
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    body: SetEnvironmentCachePolicyRequest,
    environment: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/cache-policy".format(
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
    *,
    client: AuthenticatedClient | Client,
    body: SetEnvironmentCachePolicyRequest,
    environment: str,
) -> Response[EnvironmentCachePolicy | Problem]:
    """Replace environment cache rules or restore app inheritance.

     Replaces only cache rules for the selected immutable environment host.
    An explicit empty list disables caching there. No other environment,
    default host, rule kind or app-owned rule is mutated. Native account,
    app and registry checks and the expected revision guard are atomic
    with a durable request_id receipt. Retry the identical request_id and
    body after an ambiguous response; conflicting key reuse returns 409.
    A deleted and recreated environment never inherits an old receipt.
    Gateways reload desired rules on their normal 30-second policy TTL.
    Revision-scoped rule IDs partition response-cache entries after reload.
    The receipt describes desired policy, not gateway convergence. Set
    inherited=true with rules=[] to restore live app inheritance at a new
    positive revision. The inherited rules are evaluated on gateway reload;
    the review does not freeze their contents. Omitted/false inherited with
    rules=[] still disables caching. Restoring inheritance partitions old
    response entries and is observable by the convergence endpoint.
    Optional copy_from pins an explicit override on a different named
    environment of the same app/project. Its immutable identity, revision
    and normalized rules are checked in the destination write transaction.
    Inherited sources are rejected; an explicit empty source disables cache
    at the destination. Source policy is unchanged; this is a one-time copy.
    Source identity is checked on retries, while an existing receipt can
    recover after source or destination revision changes.
    Optional restore_revision restores a retained policy snapshot as a new
    revision. The inherited mode and rules must match that snapshot in the
    same guarded write transaction; zero selects initial live inheritance.
    It cannot be combined with copy_from. Intent, history and the retry
    receipt commit atomically. A rollback never decrements the current
    revision or overwrites a concurrent edit.

    Args:
        slug (str):
        environment (str):
        body (SetEnvironmentCachePolicyRequest): Complete named-environment replacement with
            immutable identity, revision guard and transactional retry ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePolicy | Problem]
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
    body: SetEnvironmentCachePolicyRequest,
    environment: str,
) -> EnvironmentCachePolicy | Problem | None:
    """Replace environment cache rules or restore app inheritance.

     Replaces only cache rules for the selected immutable environment host.
    An explicit empty list disables caching there. No other environment,
    default host, rule kind or app-owned rule is mutated. Native account,
    app and registry checks and the expected revision guard are atomic
    with a durable request_id receipt. Retry the identical request_id and
    body after an ambiguous response; conflicting key reuse returns 409.
    A deleted and recreated environment never inherits an old receipt.
    Gateways reload desired rules on their normal 30-second policy TTL.
    Revision-scoped rule IDs partition response-cache entries after reload.
    The receipt describes desired policy, not gateway convergence. Set
    inherited=true with rules=[] to restore live app inheritance at a new
    positive revision. The inherited rules are evaluated on gateway reload;
    the review does not freeze their contents. Omitted/false inherited with
    rules=[] still disables caching. Restoring inheritance partitions old
    response entries and is observable by the convergence endpoint.
    Optional copy_from pins an explicit override on a different named
    environment of the same app/project. Its immutable identity, revision
    and normalized rules are checked in the destination write transaction.
    Inherited sources are rejected; an explicit empty source disables cache
    at the destination. Source policy is unchanged; this is a one-time copy.
    Source identity is checked on retries, while an existing receipt can
    recover after source or destination revision changes.
    Optional restore_revision restores a retained policy snapshot as a new
    revision. The inherited mode and rules must match that snapshot in the
    same guarded write transaction; zero selects initial live inheritance.
    It cannot be combined with copy_from. Intent, history and the retry
    receipt commit atomically. A rollback never decrements the current
    revision or overwrites a concurrent edit.

    Args:
        slug (str):
        environment (str):
        body (SetEnvironmentCachePolicyRequest): Complete named-environment replacement with
            immutable identity, revision guard and transactional retry ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePolicy | Problem
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
    body: SetEnvironmentCachePolicyRequest,
    environment: str,
) -> Response[EnvironmentCachePolicy | Problem]:
    """Replace environment cache rules or restore app inheritance.

     Replaces only cache rules for the selected immutable environment host.
    An explicit empty list disables caching there. No other environment,
    default host, rule kind or app-owned rule is mutated. Native account,
    app and registry checks and the expected revision guard are atomic
    with a durable request_id receipt. Retry the identical request_id and
    body after an ambiguous response; conflicting key reuse returns 409.
    A deleted and recreated environment never inherits an old receipt.
    Gateways reload desired rules on their normal 30-second policy TTL.
    Revision-scoped rule IDs partition response-cache entries after reload.
    The receipt describes desired policy, not gateway convergence. Set
    inherited=true with rules=[] to restore live app inheritance at a new
    positive revision. The inherited rules are evaluated on gateway reload;
    the review does not freeze their contents. Omitted/false inherited with
    rules=[] still disables caching. Restoring inheritance partitions old
    response entries and is observable by the convergence endpoint.
    Optional copy_from pins an explicit override on a different named
    environment of the same app/project. Its immutable identity, revision
    and normalized rules are checked in the destination write transaction.
    Inherited sources are rejected; an explicit empty source disables cache
    at the destination. Source policy is unchanged; this is a one-time copy.
    Source identity is checked on retries, while an existing receipt can
    recover after source or destination revision changes.
    Optional restore_revision restores a retained policy snapshot as a new
    revision. The inherited mode and rules must match that snapshot in the
    same guarded write transaction; zero selects initial live inheritance.
    It cannot be combined with copy_from. Intent, history and the retry
    receipt commit atomically. A rollback never decrements the current
    revision or overwrites a concurrent edit.

    Args:
        slug (str):
        environment (str):
        body (SetEnvironmentCachePolicyRequest): Complete named-environment replacement with
            immutable identity, revision guard and transactional retry ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCachePolicy | Problem]
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
    body: SetEnvironmentCachePolicyRequest,
    environment: str,
) -> EnvironmentCachePolicy | Problem | None:
    """Replace environment cache rules or restore app inheritance.

     Replaces only cache rules for the selected immutable environment host.
    An explicit empty list disables caching there. No other environment,
    default host, rule kind or app-owned rule is mutated. Native account,
    app and registry checks and the expected revision guard are atomic
    with a durable request_id receipt. Retry the identical request_id and
    body after an ambiguous response; conflicting key reuse returns 409.
    A deleted and recreated environment never inherits an old receipt.
    Gateways reload desired rules on their normal 30-second policy TTL.
    Revision-scoped rule IDs partition response-cache entries after reload.
    The receipt describes desired policy, not gateway convergence. Set
    inherited=true with rules=[] to restore live app inheritance at a new
    positive revision. The inherited rules are evaluated on gateway reload;
    the review does not freeze their contents. Omitted/false inherited with
    rules=[] still disables caching. Restoring inheritance partitions old
    response entries and is observable by the convergence endpoint.
    Optional copy_from pins an explicit override on a different named
    environment of the same app/project. Its immutable identity, revision
    and normalized rules are checked in the destination write transaction.
    Inherited sources are rejected; an explicit empty source disables cache
    at the destination. Source policy is unchanged; this is a one-time copy.
    Source identity is checked on retries, while an existing receipt can
    recover after source or destination revision changes.
    Optional restore_revision restores a retained policy snapshot as a new
    revision. The inherited mode and rules must match that snapshot in the
    same guarded write transaction; zero selects initial live inheritance.
    It cannot be combined with copy_from. Intent, history and the retry
    receipt commit atomically. A rollback never decrements the current
    revision or overwrites a concurrent edit.

    Args:
        slug (str):
        environment (str):
        body (SetEnvironmentCachePolicyRequest): Complete named-environment replacement with
            immutable identity, revision guard and transactional retry ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCachePolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            environment=environment,
        )
    ).parsed
