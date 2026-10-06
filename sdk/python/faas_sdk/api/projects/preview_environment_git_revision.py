from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.preview_environment_git_revision_request import PreviewEnvironmentGitRevisionRequest
from ...models.preview_environment_git_revision_response import PreviewEnvironmentGitRevisionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: PreviewEnvironmentGitRevisionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/gitops/revisions/preview".format(
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
) -> PreviewEnvironmentGitRevisionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PreviewEnvironmentGitRevisionResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PreviewEnvironmentGitRevisionResponse | Problem]:
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
    body: PreviewEnvironmentGitRevisionRequest,
) -> Response[PreviewEnvironmentGitRevisionResponse | Problem]:
    """Fetch an immutable commit and review its definition digest.

    Args:
        slug (str):
        environment (str):
        body (PreviewEnvironmentGitRevisionRequest): An exact GitHub commit to fetch and review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreviewEnvironmentGitRevisionResponse | Problem]
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
    body: PreviewEnvironmentGitRevisionRequest,
) -> PreviewEnvironmentGitRevisionResponse | Problem | None:
    """Fetch an immutable commit and review its definition digest.

    Args:
        slug (str):
        environment (str):
        body (PreviewEnvironmentGitRevisionRequest): An exact GitHub commit to fetch and review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreviewEnvironmentGitRevisionResponse | Problem
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
    body: PreviewEnvironmentGitRevisionRequest,
) -> Response[PreviewEnvironmentGitRevisionResponse | Problem]:
    """Fetch an immutable commit and review its definition digest.

    Args:
        slug (str):
        environment (str):
        body (PreviewEnvironmentGitRevisionRequest): An exact GitHub commit to fetch and review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreviewEnvironmentGitRevisionResponse | Problem]
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
    body: PreviewEnvironmentGitRevisionRequest,
) -> PreviewEnvironmentGitRevisionResponse | Problem | None:
    """Fetch an immutable commit and review its definition digest.

    Args:
        slug (str):
        environment (str):
        body (PreviewEnvironmentGitRevisionRequest): An exact GitHub commit to fetch and review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreviewEnvironmentGitRevisionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
