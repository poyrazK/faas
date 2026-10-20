from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_upstream_response import DataUpstreamResponse
from ...models.problem import Problem
from ...models.put_data_upstream_request import PutDataUpstreamRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: PutDataUpstreamRequest,
    deployment_scope: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["deployment_scope"] = deployment_scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/upstreams".format(
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
) -> DataUpstreamResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DataUpstreamResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataUpstreamResponse | Problem]:
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
    body: PutDataUpstreamRequest,
    deployment_scope: str | Unset = UNSET,
) -> Response[DataUpstreamResponse | Problem]:
    """Add a data upstream to an app.

     Captures a (host, kind, port) tuple so the meterd probe loop
    can dial it (PR-C) and schedd can bias wake placement by the
    probed RTT (PR-D). Plaintext host is hashed via
    `sha256(HostHashSalt||host)` before insert; the response
    returns the hashed form only (§11 invariant).

    **Plan limits.** Free plan returns 402
    `plan_data_upstreams_not_allowed`. Hobby/Pro/Scale hit their
    per-app cap (3/10/50) before the request body is parsed —
    server returns 403 `plan_limit_data_upstreams` with the
    observed count. Invalid inputs return 400
    `upstream_invalid_{kind,host,port}`.

    Args:
        slug (str):
        deployment_scope (str | Unset):
        body (PutDataUpstreamRequest): Upsert payload for a customer data upstream. The (kind,
            host, port,
            scope, deployment_scope) tuple is the deduplication key — repeating
            the PUT updates the existing row's `last_seen_at` and (if
            `FAAS_DATA_PLACEMENT=1`) the inferred-source tag. Plaintext host is
            never persisted; the on-disk column is `host_redacted_hash`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataUpstreamResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        deployment_scope=deployment_scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutDataUpstreamRequest,
    deployment_scope: str | Unset = UNSET,
) -> DataUpstreamResponse | Problem | None:
    """Add a data upstream to an app.

     Captures a (host, kind, port) tuple so the meterd probe loop
    can dial it (PR-C) and schedd can bias wake placement by the
    probed RTT (PR-D). Plaintext host is hashed via
    `sha256(HostHashSalt||host)` before insert; the response
    returns the hashed form only (§11 invariant).

    **Plan limits.** Free plan returns 402
    `plan_data_upstreams_not_allowed`. Hobby/Pro/Scale hit their
    per-app cap (3/10/50) before the request body is parsed —
    server returns 403 `plan_limit_data_upstreams` with the
    observed count. Invalid inputs return 400
    `upstream_invalid_{kind,host,port}`.

    Args:
        slug (str):
        deployment_scope (str | Unset):
        body (PutDataUpstreamRequest): Upsert payload for a customer data upstream. The (kind,
            host, port,
            scope, deployment_scope) tuple is the deduplication key — repeating
            the PUT updates the existing row's `last_seen_at` and (if
            `FAAS_DATA_PLACEMENT=1`) the inferred-source tag. Plaintext host is
            never persisted; the on-disk column is `host_redacted_hash`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataUpstreamResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        deployment_scope=deployment_scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutDataUpstreamRequest,
    deployment_scope: str | Unset = UNSET,
) -> Response[DataUpstreamResponse | Problem]:
    """Add a data upstream to an app.

     Captures a (host, kind, port) tuple so the meterd probe loop
    can dial it (PR-C) and schedd can bias wake placement by the
    probed RTT (PR-D). Plaintext host is hashed via
    `sha256(HostHashSalt||host)` before insert; the response
    returns the hashed form only (§11 invariant).

    **Plan limits.** Free plan returns 402
    `plan_data_upstreams_not_allowed`. Hobby/Pro/Scale hit their
    per-app cap (3/10/50) before the request body is parsed —
    server returns 403 `plan_limit_data_upstreams` with the
    observed count. Invalid inputs return 400
    `upstream_invalid_{kind,host,port}`.

    Args:
        slug (str):
        deployment_scope (str | Unset):
        body (PutDataUpstreamRequest): Upsert payload for a customer data upstream. The (kind,
            host, port,
            scope, deployment_scope) tuple is the deduplication key — repeating
            the PUT updates the existing row's `last_seen_at` and (if
            `FAAS_DATA_PLACEMENT=1`) the inferred-source tag. Plaintext host is
            never persisted; the on-disk column is `host_redacted_hash`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataUpstreamResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        deployment_scope=deployment_scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutDataUpstreamRequest,
    deployment_scope: str | Unset = UNSET,
) -> DataUpstreamResponse | Problem | None:
    """Add a data upstream to an app.

     Captures a (host, kind, port) tuple so the meterd probe loop
    can dial it (PR-C) and schedd can bias wake placement by the
    probed RTT (PR-D). Plaintext host is hashed via
    `sha256(HostHashSalt||host)` before insert; the response
    returns the hashed form only (§11 invariant).

    **Plan limits.** Free plan returns 402
    `plan_data_upstreams_not_allowed`. Hobby/Pro/Scale hit their
    per-app cap (3/10/50) before the request body is parsed —
    server returns 403 `plan_limit_data_upstreams` with the
    observed count. Invalid inputs return 400
    `upstream_invalid_{kind,host,port}`.

    Args:
        slug (str):
        deployment_scope (str | Unset):
        body (PutDataUpstreamRequest): Upsert payload for a customer data upstream. The (kind,
            host, port,
            scope, deployment_scope) tuple is the deduplication key — repeating
            the PUT updates the existing row's `last_seen_at` and (if
            `FAAS_DATA_PLACEMENT=1`) the inferred-source tag. Plaintext host is
            never persisted; the on-disk column is `host_redacted_hash`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataUpstreamResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            deployment_scope=deployment_scope,
        )
    ).parsed
