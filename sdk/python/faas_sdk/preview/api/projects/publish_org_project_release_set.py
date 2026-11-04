from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_release_set_response import ProjectReleaseSetResponse
from ...models.publish_project_release_set_request import PublishProjectReleaseSetRequest
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    environment: str,
    *,
    body: PublishProjectReleaseSetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets".format(
            organization=quote(str(organization), safe=""),
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
) -> Problem | ProjectReleaseSetResponse | None:
    if response.status_code == 201:
        response_201 = ProjectReleaseSetResponse.from_dict(response.json())

        return response_201

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
) -> Response[Problem | ProjectReleaseSetResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Response[Problem | ProjectReleaseSetResponse]:
    """Activate an immutable organization project release graph.

     Every company workload must have a live deployment with revision pinning that covers the requested
    TTL. Earlier organization release sets stay addressable until expiry.

    Args:
        organization (str):
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectReleaseSetResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Problem | ProjectReleaseSetResponse | None:
    """Activate an immutable organization project release graph.

     Every company workload must have a live deployment with revision pinning that covers the requested
    TTL. Earlier organization release sets stay addressable until expiry.

    Args:
        organization (str):
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectReleaseSetResponse
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Response[Problem | ProjectReleaseSetResponse]:
    """Activate an immutable organization project release graph.

     Every company workload must have a live deployment with revision pinning that covers the requested
    TTL. Earlier organization release sets stay addressable until expiry.

    Args:
        organization (str):
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectReleaseSetResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Problem | ProjectReleaseSetResponse | None:
    """Activate an immutable organization project release graph.

     Every company workload must have a live deployment with revision pinning that covers the requested
    TTL. Earlier organization release sets stay addressable until expiry.

    Args:
        organization (str):
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectReleaseSetResponse
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
