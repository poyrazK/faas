from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_project_environment_qualification_request import CreateProjectEnvironmentQualificationRequest
from ...models.problem import Problem
from ...models.project_environment_qualification_response import ProjectEnvironmentQualificationResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: CreateProjectEnvironmentQualificationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/qualifications".format(
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
) -> Problem | ProjectEnvironmentQualificationResponse | None:
    if response.status_code == 201:
        response_201 = ProjectEnvironmentQualificationResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | ProjectEnvironmentQualificationResponse]:
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
    body: CreateProjectEnvironmentQualificationRequest,
) -> Response[Problem | ProjectEnvironmentQualificationResponse]:
    """Record health and smoke results for an active release set.

     Records a closed-schema qualification receipt for the exact active
    release-set ID, non-secret source configuration version/hash, and
    per-workload secret revision fingerprints. Fingerprints include only
    revision metadata and managed credential generations, never secret
    values or value hashes.
    Each health and smoke result identifies every workload's exact
    deployment and contains only its HTTP status or a bounded error code;
    response bodies and secrets are never stored. The API rejects probes
    if the release set, configuration, or secret revisions change before
    receipt creation.
    Receipts expire after 24 hours and cannot qualify a later release set
    or configuration or secret revision.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentQualificationRequest): Closed-schema health and smoke probe
            results for one exact active source release set, non-secret source configuration version,
            and per-workload secret revision fingerprints.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentQualificationResponse]
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
    body: CreateProjectEnvironmentQualificationRequest,
) -> Problem | ProjectEnvironmentQualificationResponse | None:
    """Record health and smoke results for an active release set.

     Records a closed-schema qualification receipt for the exact active
    release-set ID, non-secret source configuration version/hash, and
    per-workload secret revision fingerprints. Fingerprints include only
    revision metadata and managed credential generations, never secret
    values or value hashes.
    Each health and smoke result identifies every workload's exact
    deployment and contains only its HTTP status or a bounded error code;
    response bodies and secrets are never stored. The API rejects probes
    if the release set, configuration, or secret revisions change before
    receipt creation.
    Receipts expire after 24 hours and cannot qualify a later release set
    or configuration or secret revision.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentQualificationRequest): Closed-schema health and smoke probe
            results for one exact active source release set, non-secret source configuration version,
            and per-workload secret revision fingerprints.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentQualificationResponse
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
    body: CreateProjectEnvironmentQualificationRequest,
) -> Response[Problem | ProjectEnvironmentQualificationResponse]:
    """Record health and smoke results for an active release set.

     Records a closed-schema qualification receipt for the exact active
    release-set ID, non-secret source configuration version/hash, and
    per-workload secret revision fingerprints. Fingerprints include only
    revision metadata and managed credential generations, never secret
    values or value hashes.
    Each health and smoke result identifies every workload's exact
    deployment and contains only its HTTP status or a bounded error code;
    response bodies and secrets are never stored. The API rejects probes
    if the release set, configuration, or secret revisions change before
    receipt creation.
    Receipts expire after 24 hours and cannot qualify a later release set
    or configuration or secret revision.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentQualificationRequest): Closed-schema health and smoke probe
            results for one exact active source release set, non-secret source configuration version,
            and per-workload secret revision fingerprints.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentQualificationResponse]
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
    body: CreateProjectEnvironmentQualificationRequest,
) -> Problem | ProjectEnvironmentQualificationResponse | None:
    """Record health and smoke results for an active release set.

     Records a closed-schema qualification receipt for the exact active
    release-set ID, non-secret source configuration version/hash, and
    per-workload secret revision fingerprints. Fingerprints include only
    revision metadata and managed credential generations, never secret
    values or value hashes.
    Each health and smoke result identifies every workload's exact
    deployment and contains only its HTTP status or a bounded error code;
    response bodies and secrets are never stored. The API rejects probes
    if the release set, configuration, or secret revisions change before
    receipt creation.
    Receipts expire after 24 hours and cannot qualify a later release set
    or configuration or secret revision.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentQualificationRequest): Closed-schema health and smoke probe
            results for one exact active source release set, non-secret source configuration version,
            and per-workload secret revision fingerprints.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentQualificationResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
