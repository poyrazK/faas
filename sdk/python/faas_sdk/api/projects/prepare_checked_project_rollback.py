from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.checked_project_release_manifest import CheckedProjectReleaseManifest
from ...models.prepare_project_rollback_request import PrepareProjectRollbackRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: PrepareProjectRollbackRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/prepare-rollback".format(
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
) -> CheckedProjectReleaseManifest | Problem | None:
    if response.status_code == 200:
        response_200 = CheckedProjectReleaseManifest.from_dict(response.json())

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
) -> Response[CheckedProjectReleaseManifest | Problem]:
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
    body: PrepareProjectRollbackRequest,
) -> Response[CheckedProjectReleaseManifest | Problem]:
    """Prepare checked release-set recovery to a selected retained graph

     Checked release-set rollback preparation. Selects an inactive, unexpired historical release in this
    project environment.
    Requires a different expected active release, exact historical membership,
    current enforced policies, fresh target-specific evidence and ready artifacts
    and service replicas. Historical expiry is checked again at commit.
    Publishes a new immutable release recording rollback_source_release_id and
    rollback_previous_release_id; history never grants a policy waiver.
    Use an Idempotency-Key for mutation retries. No deployments are awakened.

    Args:
        slug (str):
        environment (str):
        body (PrepareProjectRollbackRequest): Historical release selection used to prepare a
            checked project rollback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CheckedProjectReleaseManifest | Problem]
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
    body: PrepareProjectRollbackRequest,
) -> CheckedProjectReleaseManifest | Problem | None:
    """Prepare checked release-set recovery to a selected retained graph

     Checked release-set rollback preparation. Selects an inactive, unexpired historical release in this
    project environment.
    Requires a different expected active release, exact historical membership,
    current enforced policies, fresh target-specific evidence and ready artifacts
    and service replicas. Historical expiry is checked again at commit.
    Publishes a new immutable release recording rollback_source_release_id and
    rollback_previous_release_id; history never grants a policy waiver.
    Use an Idempotency-Key for mutation retries. No deployments are awakened.

    Args:
        slug (str):
        environment (str):
        body (PrepareProjectRollbackRequest): Historical release selection used to prepare a
            checked project rollback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CheckedProjectReleaseManifest | Problem
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
    body: PrepareProjectRollbackRequest,
) -> Response[CheckedProjectReleaseManifest | Problem]:
    """Prepare checked release-set recovery to a selected retained graph

     Checked release-set rollback preparation. Selects an inactive, unexpired historical release in this
    project environment.
    Requires a different expected active release, exact historical membership,
    current enforced policies, fresh target-specific evidence and ready artifacts
    and service replicas. Historical expiry is checked again at commit.
    Publishes a new immutable release recording rollback_source_release_id and
    rollback_previous_release_id; history never grants a policy waiver.
    Use an Idempotency-Key for mutation retries. No deployments are awakened.

    Args:
        slug (str):
        environment (str):
        body (PrepareProjectRollbackRequest): Historical release selection used to prepare a
            checked project rollback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CheckedProjectReleaseManifest | Problem]
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
    body: PrepareProjectRollbackRequest,
) -> CheckedProjectReleaseManifest | Problem | None:
    """Prepare checked release-set recovery to a selected retained graph

     Checked release-set rollback preparation. Selects an inactive, unexpired historical release in this
    project environment.
    Requires a different expected active release, exact historical membership,
    current enforced policies, fresh target-specific evidence and ready artifacts
    and service replicas. Historical expiry is checked again at commit.
    Publishes a new immutable release recording rollback_source_release_id and
    rollback_previous_release_id; history never grants a policy waiver.
    Use an Idempotency-Key for mutation retries. No deployments are awakened.

    Args:
        slug (str):
        environment (str):
        body (PrepareProjectRollbackRequest): Historical release selection used to prepare a
            checked project rollback.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CheckedProjectReleaseManifest | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
