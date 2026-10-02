from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_health_report import RouteHealthReport
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-health/deployments/{deployment}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteHealthReport | None:
    if response.status_code == 200:
        response_200 = RouteHealthReport.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteHealthReport]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteHealthReport]:
    """Compare observed critical route errors and optional p95 latency for candidate and stable
    deployments.

     Requires apps:read or admin and completed MFA. Compares exact normalized telemetry labels and
    weighted counts in two consecutive closed UTC minute windows, behind a 30 second ingestion
    allowance. Error checks need 20 represented requests on each deployment per window. Selected latency
    checks need 100, with p95 estimates weighted by collapsed telemetry counts. A positive max_p95_ms is
    an absolute candidate budget; check_latency independently checks for at least 1.5 times stable p95
    and at least 100 ms additional latency. Each signal is confirmed independently across both windows.
    Both windows must begin after the current stage and latest configuration update. Missing, sparse,
    ambiguous or unavailable evidence is unknown. Coverage is observed_only; full capture and requests
    dropped before storage cannot be established. Enforce mode pauses subsequent advances unless every
    selected route is healthy; never automatically aborts. Stable deployment is the sole other live
    serving deployment in the same scope.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthReport]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteHealthReport | None:
    """Compare observed critical route errors and optional p95 latency for candidate and stable
    deployments.

     Requires apps:read or admin and completed MFA. Compares exact normalized telemetry labels and
    weighted counts in two consecutive closed UTC minute windows, behind a 30 second ingestion
    allowance. Error checks need 20 represented requests on each deployment per window. Selected latency
    checks need 100, with p95 estimates weighted by collapsed telemetry counts. A positive max_p95_ms is
    an absolute candidate budget; check_latency independently checks for at least 1.5 times stable p95
    and at least 100 ms additional latency. Each signal is confirmed independently across both windows.
    Both windows must begin after the current stage and latest configuration update. Missing, sparse,
    ambiguous or unavailable evidence is unknown. Coverage is observed_only; full capture and requests
    dropped before storage cannot be established. Enforce mode pauses subsequent advances unless every
    selected route is healthy; never automatically aborts. Stable deployment is the sole other live
    serving deployment in the same scope.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthReport
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteHealthReport]:
    """Compare observed critical route errors and optional p95 latency for candidate and stable
    deployments.

     Requires apps:read or admin and completed MFA. Compares exact normalized telemetry labels and
    weighted counts in two consecutive closed UTC minute windows, behind a 30 second ingestion
    allowance. Error checks need 20 represented requests on each deployment per window. Selected latency
    checks need 100, with p95 estimates weighted by collapsed telemetry counts. A positive max_p95_ms is
    an absolute candidate budget; check_latency independently checks for at least 1.5 times stable p95
    and at least 100 ms additional latency. Each signal is confirmed independently across both windows.
    Both windows must begin after the current stage and latest configuration update. Missing, sparse,
    ambiguous or unavailable evidence is unknown. Coverage is observed_only; full capture and requests
    dropped before storage cannot be established. Enforce mode pauses subsequent advances unless every
    selected route is healthy; never automatically aborts. Stable deployment is the sole other live
    serving deployment in the same scope.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthReport]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteHealthReport | None:
    """Compare observed critical route errors and optional p95 latency for candidate and stable
    deployments.

     Requires apps:read or admin and completed MFA. Compares exact normalized telemetry labels and
    weighted counts in two consecutive closed UTC minute windows, behind a 30 second ingestion
    allowance. Error checks need 20 represented requests on each deployment per window. Selected latency
    checks need 100, with p95 estimates weighted by collapsed telemetry counts. A positive max_p95_ms is
    an absolute candidate budget; check_latency independently checks for at least 1.5 times stable p95
    and at least 100 ms additional latency. Each signal is confirmed independently across both windows.
    Both windows must begin after the current stage and latest configuration update. Missing, sparse,
    ambiguous or unavailable evidence is unknown. Coverage is observed_only; full capture and requests
    dropped before storage cannot be established. Enforce mode pauses subsequent advances unless every
    selected route is healthy; never automatically aborts. Stable deployment is the sole other live
    serving deployment in the same scope.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthReport
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
        )
    ).parsed
