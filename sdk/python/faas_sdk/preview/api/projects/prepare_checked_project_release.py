from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.checked_project_release_manifest import CheckedProjectReleaseManifest
from ...models.problem import Problem
from ...models.publish_project_release_set_request import PublishProjectReleaseSetRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: PublishProjectReleaseSetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/prepare".format(
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
    body: PublishProjectReleaseSetRequest,
) -> Response[CheckedProjectReleaseManifest | Problem]:
    """Prepare a project release-set manifest for checked binding qualification

     Checked release-set preparation. Requires an enforced binding release policy on every project member
    scope.
    Preparation freezes exact deployments, active graph or weighted fallback,
    policy revisions and internal service targets. It is a metadata observation,
    not an evidence grant. Qualification uses candidate-specific task probes.
    Activation independently checks all member evidence, service edges, artifacts,
    full project membership, current policy, evidence expiry, active graph CAS
    and service replica readiness in one transaction. A blocked graph keeps
    current routes. No target handler calls or wakes are made by service probes.
    Generic graph publication and deactivation remain fail closed under enforcement.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

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
    body: PublishProjectReleaseSetRequest,
) -> CheckedProjectReleaseManifest | Problem | None:
    """Prepare a project release-set manifest for checked binding qualification

     Checked release-set preparation. Requires an enforced binding release policy on every project member
    scope.
    Preparation freezes exact deployments, active graph or weighted fallback,
    policy revisions and internal service targets. It is a metadata observation,
    not an evidence grant. Qualification uses candidate-specific task probes.
    Activation independently checks all member evidence, service edges, artifacts,
    full project membership, current policy, evidence expiry, active graph CAS
    and service replica readiness in one transaction. A blocked graph keeps
    current routes. No target handler calls or wakes are made by service probes.
    Generic graph publication and deactivation remain fail closed under enforcement.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

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
    body: PublishProjectReleaseSetRequest,
) -> Response[CheckedProjectReleaseManifest | Problem]:
    """Prepare a project release-set manifest for checked binding qualification

     Checked release-set preparation. Requires an enforced binding release policy on every project member
    scope.
    Preparation freezes exact deployments, active graph or weighted fallback,
    policy revisions and internal service targets. It is a metadata observation,
    not an evidence grant. Qualification uses candidate-specific task probes.
    Activation independently checks all member evidence, service edges, artifacts,
    full project membership, current policy, evidence expiry, active graph CAS
    and service replica readiness in one transaction. A blocked graph keeps
    current routes. No target handler calls or wakes are made by service probes.
    Generic graph publication and deactivation remain fail closed under enforcement.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

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
    body: PublishProjectReleaseSetRequest,
) -> CheckedProjectReleaseManifest | Problem | None:
    """Prepare a project release-set manifest for checked binding qualification

     Checked release-set preparation. Requires an enforced binding release policy on every project member
    scope.
    Preparation freezes exact deployments, active graph or weighted fallback,
    policy revisions and internal service targets. It is a metadata observation,
    not an evidence grant. Qualification uses candidate-specific task probes.
    Activation independently checks all member evidence, service edges, artifacts,
    full project membership, current policy, evidence expiry, active graph CAS
    and service replica readiness in one transaction. A blocked graph keeps
    current routes. No target handler calls or wakes are made by service probes.
    Generic graph publication and deactivation remain fail closed under enforcement.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

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
