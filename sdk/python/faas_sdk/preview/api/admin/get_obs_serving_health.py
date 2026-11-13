from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_serving_health import ObsServingHealth
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/serving-health",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsServingHealth | Problem | None:
    if response.status_code == 200:
        response_200 = ObsServingHealth.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObsServingHealth | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsServingHealth | Problem]:
    """Inspect fleet serving-probe coverage against deployment intent.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters and performs no writes or direct HTTP probe. Four strict
    Prometheus queries share one pinned evaluation time and three-second
    budget. Independent inventory identifies expected node/URL pairs; missing,
    stale, inconsistent or unconfigured inventory never becomes healthy empty
    coverage. An explicitly configured empty fleet is not_applicable.
    Fresh deployed coverage-rule evaluations distinguish healthy, missing,
    stale, invalid telemetry, disabled content validation, latency budget
    mismatch and failing probes. Missing or old evaluations remain unknown.
    Recorded policy must match current intent and evaluation timestamps must
    agree. A healthy evaluation cannot hold green beyond its run age budget.
    Known faults take precedence over unknown peer evaluations.
    Returns at most 1000 expected pairs ordered by stable node name and URL
    fingerprint. URL IDs are lowercase SHA-256 of exact configured URL bytes.
    Raw URLs, response bodies, scrape addresses, source labels and provider
    errors are excluded. Recording-rule semantics are trusted; this read does
    not qualify notification delivery or independent watchdog receipt.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsServingHealth | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsServingHealth | Problem | None:
    """Inspect fleet serving-probe coverage against deployment intent.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters and performs no writes or direct HTTP probe. Four strict
    Prometheus queries share one pinned evaluation time and three-second
    budget. Independent inventory identifies expected node/URL pairs; missing,
    stale, inconsistent or unconfigured inventory never becomes healthy empty
    coverage. An explicitly configured empty fleet is not_applicable.
    Fresh deployed coverage-rule evaluations distinguish healthy, missing,
    stale, invalid telemetry, disabled content validation, latency budget
    mismatch and failing probes. Missing or old evaluations remain unknown.
    Recorded policy must match current intent and evaluation timestamps must
    agree. A healthy evaluation cannot hold green beyond its run age budget.
    Known faults take precedence over unknown peer evaluations.
    Returns at most 1000 expected pairs ordered by stable node name and URL
    fingerprint. URL IDs are lowercase SHA-256 of exact configured URL bytes.
    Raw URLs, response bodies, scrape addresses, source labels and provider
    errors are excluded. Recording-rule semantics are trusted; this read does
    not qualify notification delivery or independent watchdog receipt.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsServingHealth | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsServingHealth | Problem]:
    """Inspect fleet serving-probe coverage against deployment intent.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters and performs no writes or direct HTTP probe. Four strict
    Prometheus queries share one pinned evaluation time and three-second
    budget. Independent inventory identifies expected node/URL pairs; missing,
    stale, inconsistent or unconfigured inventory never becomes healthy empty
    coverage. An explicitly configured empty fleet is not_applicable.
    Fresh deployed coverage-rule evaluations distinguish healthy, missing,
    stale, invalid telemetry, disabled content validation, latency budget
    mismatch and failing probes. Missing or old evaluations remain unknown.
    Recorded policy must match current intent and evaluation timestamps must
    agree. A healthy evaluation cannot hold green beyond its run age budget.
    Known faults take precedence over unknown peer evaluations.
    Returns at most 1000 expected pairs ordered by stable node name and URL
    fingerprint. URL IDs are lowercase SHA-256 of exact configured URL bytes.
    Raw URLs, response bodies, scrape addresses, source labels and provider
    errors are excluded. Recording-rule semantics are trusted; this read does
    not qualify notification delivery or independent watchdog receipt.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsServingHealth | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsServingHealth | Problem | None:
    """Inspect fleet serving-probe coverage against deployment intent.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters and performs no writes or direct HTTP probe. Four strict
    Prometheus queries share one pinned evaluation time and three-second
    budget. Independent inventory identifies expected node/URL pairs; missing,
    stale, inconsistent or unconfigured inventory never becomes healthy empty
    coverage. An explicitly configured empty fleet is not_applicable.
    Fresh deployed coverage-rule evaluations distinguish healthy, missing,
    stale, invalid telemetry, disabled content validation, latency budget
    mismatch and failing probes. Missing or old evaluations remain unknown.
    Recorded policy must match current intent and evaluation timestamps must
    agree. A healthy evaluation cannot hold green beyond its run age budget.
    Known faults take precedence over unknown peer evaluations.
    Returns at most 1000 expected pairs ordered by stable node name and URL
    fingerprint. URL IDs are lowercase SHA-256 of exact configured URL bytes.
    Raw URLs, response bodies, scrape addresses, source labels and provider
    errors are excluded. Recording-rule semantics are trusted; this read does
    not qualify notification delivery or independent watchdog receipt.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsServingHealth | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
