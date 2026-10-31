from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_environment_git_releases_response import ListEnvironmentGitReleasesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    target_commit_sha: str,
    before: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["target_commit_sha"] = target_commit_sha

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = str(before)
    params["before"] = json_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/gitops/releases".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListEnvironmentGitReleasesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListEnvironmentGitReleasesResponse.from_dict(response.json())

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
) -> Response[ListEnvironmentGitReleasesResponse | Problem]:
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
    target_commit_sha: str,
    before: UUID | Unset = UNSET,
) -> Response[ListEnvironmentGitReleasesResponse | Problem]:
    """List retained GitOps serving releases and current rollback eligibility.

     Verifies the immutable target manifest commit through the bound provider and assesses each release
    using the rollback planner on one scoped observation snapshot per page. Only completed serving
    history of the current source is listed. Eligibility can change after observation. Release
    compatibility deadlines do not guarantee artifact retention. This read-only catalog never pins
    artifacts or authorizes rollback execution.

    Args:
        slug (str):
        environment (str):
        target_commit_sha (str):
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListEnvironmentGitReleasesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        target_commit_sha=target_commit_sha,
        before=before,
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
    target_commit_sha: str,
    before: UUID | Unset = UNSET,
) -> ListEnvironmentGitReleasesResponse | Problem | None:
    """List retained GitOps serving releases and current rollback eligibility.

     Verifies the immutable target manifest commit through the bound provider and assesses each release
    using the rollback planner on one scoped observation snapshot per page. Only completed serving
    history of the current source is listed. Eligibility can change after observation. Release
    compatibility deadlines do not guarantee artifact retention. This read-only catalog never pins
    artifacts or authorizes rollback execution.

    Args:
        slug (str):
        environment (str):
        target_commit_sha (str):
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListEnvironmentGitReleasesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        target_commit_sha=target_commit_sha,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    target_commit_sha: str,
    before: UUID | Unset = UNSET,
) -> Response[ListEnvironmentGitReleasesResponse | Problem]:
    """List retained GitOps serving releases and current rollback eligibility.

     Verifies the immutable target manifest commit through the bound provider and assesses each release
    using the rollback planner on one scoped observation snapshot per page. Only completed serving
    history of the current source is listed. Eligibility can change after observation. Release
    compatibility deadlines do not guarantee artifact retention. This read-only catalog never pins
    artifacts or authorizes rollback execution.

    Args:
        slug (str):
        environment (str):
        target_commit_sha (str):
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListEnvironmentGitReleasesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        target_commit_sha=target_commit_sha,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    target_commit_sha: str,
    before: UUID | Unset = UNSET,
) -> ListEnvironmentGitReleasesResponse | Problem | None:
    """List retained GitOps serving releases and current rollback eligibility.

     Verifies the immutable target manifest commit through the bound provider and assesses each release
    using the rollback planner on one scoped observation snapshot per page. Only completed serving
    history of the current source is listed. Eligibility can change after observation. Release
    compatibility deadlines do not guarantee artifact retention. This read-only catalog never pins
    artifacts or authorizes rollback execution.

    Args:
        slug (str):
        environment (str):
        target_commit_sha (str):
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListEnvironmentGitReleasesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            target_commit_sha=target_commit_sha,
            before=before,
        )
    ).parsed
