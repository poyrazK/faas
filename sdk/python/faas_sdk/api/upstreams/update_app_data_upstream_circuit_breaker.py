from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_upstream_response import DataUpstreamResponse
from ...models.problem import Problem
from ...models.update_upstream_circuit_breaker_request import UpdateUpstreamCircuitBreakerRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: UpdateUpstreamCircuitBreakerRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/upstreams/{id}/circuit-breaker".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
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

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateUpstreamCircuitBreakerRequest,
) -> Response[DataUpstreamResponse | Problem]:
    """Update an upstream's egress circuit-breaker policy.

     Opts one upstream into (or out of) egress circuit breaking, and
    optionally tunes its thresholds (ADR-201 §3).

    Enabling this grants the platform permission to REJECT your app's
    connections to this upstream while its circuit is open. Plaintext
    hosts never appear in the request or the response; upstreams are
    addressed by id and identified by `host_redacted_hash`.

    Plan-gated: accounts whose plan allows no egress breakers receive
    402. The per-app cap counts only ENABLED upstreams, so turning one
    off, or re-saving one that is already on, is never blocked.

    Args:
        slug (str):
        id (UUID):
        body (UpdateUpstreamCircuitBreakerRequest): Partial update of an upstream's egress
            circuit-breaker policy
            (ADR-201 §3). Omitted fields are left unchanged.

            Setting `enabled` to false leaves the threshold fields intact, so
            toggling protection off does not discard your tuning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataUpstreamResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateUpstreamCircuitBreakerRequest,
) -> DataUpstreamResponse | Problem | None:
    """Update an upstream's egress circuit-breaker policy.

     Opts one upstream into (or out of) egress circuit breaking, and
    optionally tunes its thresholds (ADR-201 §3).

    Enabling this grants the platform permission to REJECT your app's
    connections to this upstream while its circuit is open. Plaintext
    hosts never appear in the request or the response; upstreams are
    addressed by id and identified by `host_redacted_hash`.

    Plan-gated: accounts whose plan allows no egress breakers receive
    402. The per-app cap counts only ENABLED upstreams, so turning one
    off, or re-saving one that is already on, is never blocked.

    Args:
        slug (str):
        id (UUID):
        body (UpdateUpstreamCircuitBreakerRequest): Partial update of an upstream's egress
            circuit-breaker policy
            (ADR-201 §3). Omitted fields are left unchanged.

            Setting `enabled` to false leaves the threshold fields intact, so
            toggling protection off does not discard your tuning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataUpstreamResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateUpstreamCircuitBreakerRequest,
) -> Response[DataUpstreamResponse | Problem]:
    """Update an upstream's egress circuit-breaker policy.

     Opts one upstream into (or out of) egress circuit breaking, and
    optionally tunes its thresholds (ADR-201 §3).

    Enabling this grants the platform permission to REJECT your app's
    connections to this upstream while its circuit is open. Plaintext
    hosts never appear in the request or the response; upstreams are
    addressed by id and identified by `host_redacted_hash`.

    Plan-gated: accounts whose plan allows no egress breakers receive
    402. The per-app cap counts only ENABLED upstreams, so turning one
    off, or re-saving one that is already on, is never blocked.

    Args:
        slug (str):
        id (UUID):
        body (UpdateUpstreamCircuitBreakerRequest): Partial update of an upstream's egress
            circuit-breaker policy
            (ADR-201 §3). Omitted fields are left unchanged.

            Setting `enabled` to false leaves the threshold fields intact, so
            toggling protection off does not discard your tuning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataUpstreamResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateUpstreamCircuitBreakerRequest,
) -> DataUpstreamResponse | Problem | None:
    """Update an upstream's egress circuit-breaker policy.

     Opts one upstream into (or out of) egress circuit breaking, and
    optionally tunes its thresholds (ADR-201 §3).

    Enabling this grants the platform permission to REJECT your app's
    connections to this upstream while its circuit is open. Plaintext
    hosts never appear in the request or the response; upstreams are
    addressed by id and identified by `host_redacted_hash`.

    Plan-gated: accounts whose plan allows no egress breakers receive
    402. The per-app cap counts only ENABLED upstreams, so turning one
    off, or re-saving one that is already on, is never blocked.

    Args:
        slug (str):
        id (UUID):
        body (UpdateUpstreamCircuitBreakerRequest): Partial update of an upstream's egress
            circuit-breaker policy
            (ADR-201 §3). Omitted fields are left unchanged.

            Setting `enabled` to false leaves the threshold fields intact, so
            toggling protection off does not discard your tuning.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataUpstreamResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
