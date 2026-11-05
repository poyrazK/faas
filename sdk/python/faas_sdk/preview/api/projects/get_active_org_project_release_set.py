from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_release_set_response import ProjectReleaseSetResponse
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    environment: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets/active".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectReleaseSetResponse | None:
    if response.status_code == 200:
        response_200 = ProjectReleaseSetResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectReleaseSetResponse]:
    """Read the active organization project release graph.

     The addressed company environment must have an active release graph; otherwise this operation
    returns 404.

    Args:
        organization (str):
        slug (str):
        environment (str):

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
) -> Problem | ProjectReleaseSetResponse | None:
    """Read the active organization project release graph.

     The addressed company environment must have an active release graph; otherwise this operation
    returns 404.

    Args:
        organization (str):
        slug (str):
        environment (str):

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
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectReleaseSetResponse]:
    """Read the active organization project release graph.

     The addressed company environment must have an active release graph; otherwise this operation
    returns 404.

    Args:
        organization (str):
        slug (str):
        environment (str):

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
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectReleaseSetResponse | None:
    """Read the active organization project release graph.

     The addressed company environment must have an active release graph; otherwise this operation
    returns 404.

    Args:
        organization (str):
        slug (str):
        environment (str):

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
        )
    ).parsed
