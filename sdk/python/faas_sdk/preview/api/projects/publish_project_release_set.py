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
    slug: str,
    environment: str,
    *,
    body: PublishProjectReleaseSetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets".format(
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
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Response[Problem | ProjectReleaseSetResponse]:
    """Atomically activate an immutable project deployment graph.

     Every project workload must have one live deployment and revision pinning enabled for at least the
    requested TTL. Previous release sets remain addressable until expiry.

    Args:
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
) -> Problem | ProjectReleaseSetResponse | None:
    """Atomically activate an immutable project deployment graph.

     Every project workload must have one live deployment and revision pinning enabled for at least the
    requested TTL. Previous release sets remain addressable until expiry.

    Args:
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
) -> Response[Problem | ProjectReleaseSetResponse]:
    """Atomically activate an immutable project deployment graph.

     Every project workload must have one live deployment and revision pinning enabled for at least the
    requested TTL. Previous release sets remain addressable until expiry.

    Args:
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
) -> Problem | ProjectReleaseSetResponse | None:
    """Atomically activate an immutable project deployment graph.

     Every project workload must have one live deployment and revision pinning enabled for at least the
    requested TTL. Previous release sets remain addressable until expiry.

    Args:
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
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
