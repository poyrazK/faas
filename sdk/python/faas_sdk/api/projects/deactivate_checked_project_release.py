from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.checked_project_release_manifest import CheckedProjectReleaseManifest
from ...models.checked_project_release_response import CheckedProjectReleaseResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: CheckedProjectReleaseManifest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/deactivate".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CheckedProjectReleaseResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CheckedProjectReleaseResponse.from_dict(response.json())

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
) -> Response[CheckedProjectReleaseResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CheckedProjectReleaseManifest,
) -> Response[CheckedProjectReleaseResponse | Problem]:
    """Deactivate a checked project release set after verifying fallback recipients

     Requires one eligible materialized live 100% fallback per current workload,
    a current active graph and enforced binding policies on every fallback.
    Preparation observes routes without changing them. Use the returned manifest
    to qualify exact fallback service dependencies, then deactivate explicitly.
    ttl_seconds is zero because the retired graph keeps its existing TTL.
    Deactivation compares active graph, complete fallback routing, current policies,
    fresh evidence, artifacts and service readiness atomically. PostgreSQL rechecks
    routing and evidence expiry at commit. Missing routes and traffic splits block.
    The retired release contains an immutable deactivation receipt. Use an
    Idempotency-Key for retries. GitOps-managed workloads remain blocked.

    Args:
        slug (str):
        environment (str):
        body (CheckedProjectReleaseManifest): Immutable candidate graph and expected routing and
            policy state. This manifest conveys no evidence or waiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CheckedProjectReleaseResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CheckedProjectReleaseManifest,
) -> CheckedProjectReleaseResponse | Problem | None:
    """Deactivate a checked project release set after verifying fallback recipients

     Requires one eligible materialized live 100% fallback per current workload,
    a current active graph and enforced binding policies on every fallback.
    Preparation observes routes without changing them. Use the returned manifest
    to qualify exact fallback service dependencies, then deactivate explicitly.
    ttl_seconds is zero because the retired graph keeps its existing TTL.
    Deactivation compares active graph, complete fallback routing, current policies,
    fresh evidence, artifacts and service readiness atomically. PostgreSQL rechecks
    routing and evidence expiry at commit. Missing routes and traffic splits block.
    The retired release contains an immutable deactivation receipt. Use an
    Idempotency-Key for retries. GitOps-managed workloads remain blocked.

    Args:
        slug (str):
        environment (str):
        body (CheckedProjectReleaseManifest): Immutable candidate graph and expected routing and
            policy state. This manifest conveys no evidence or waiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CheckedProjectReleaseResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CheckedProjectReleaseManifest,
) -> Response[CheckedProjectReleaseResponse | Problem]:
    """Deactivate a checked project release set after verifying fallback recipients

     Requires one eligible materialized live 100% fallback per current workload,
    a current active graph and enforced binding policies on every fallback.
    Preparation observes routes without changing them. Use the returned manifest
    to qualify exact fallback service dependencies, then deactivate explicitly.
    ttl_seconds is zero because the retired graph keeps its existing TTL.
    Deactivation compares active graph, complete fallback routing, current policies,
    fresh evidence, artifacts and service readiness atomically. PostgreSQL rechecks
    routing and evidence expiry at commit. Missing routes and traffic splits block.
    The retired release contains an immutable deactivation receipt. Use an
    Idempotency-Key for retries. GitOps-managed workloads remain blocked.

    Args:
        slug (str):
        environment (str):
        body (CheckedProjectReleaseManifest): Immutable candidate graph and expected routing and
            policy state. This manifest conveys no evidence or waiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CheckedProjectReleaseResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CheckedProjectReleaseManifest,
) -> CheckedProjectReleaseResponse | Problem | None:
    """Deactivate a checked project release set after verifying fallback recipients

     Requires one eligible materialized live 100% fallback per current workload,
    a current active graph and enforced binding policies on every fallback.
    Preparation observes routes without changing them. Use the returned manifest
    to qualify exact fallback service dependencies, then deactivate explicitly.
    ttl_seconds is zero because the retired graph keeps its existing TTL.
    Deactivation compares active graph, complete fallback routing, current policies,
    fresh evidence, artifacts and service readiness atomically. PostgreSQL rechecks
    routing and evidence expiry at commit. Missing routes and traffic splits block.
    The retired release contains an immutable deactivation receipt. Use an
    Idempotency-Key for retries. GitOps-managed workloads remain blocked.

    Args:
        slug (str):
        environment (str):
        body (CheckedProjectReleaseManifest): Immutable candidate graph and expected routing and
            policy state. This manifest conveys no evidence or waiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CheckedProjectReleaseResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
