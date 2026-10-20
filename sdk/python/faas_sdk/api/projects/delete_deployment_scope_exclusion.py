from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.delete_deployment_scope_exclusion_response_200 import DeleteDeploymentScopeExclusionResponse200
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    slug2: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/projects/{slug}/exclusions/{slug2}".format(
            slug=quote(str(slug), safe=""),
            slug2=quote(str(slug2), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeleteDeploymentScopeExclusionResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = DeleteDeploymentScopeExclusionResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeleteDeploymentScopeExclusionResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeleteDeploymentScopeExclusionResponse200 | Problem]:
    r"""Drop a persisted --exclude row from deployment_scope_exclusions.

     Operator escape hatch (ADR-124 code-review fix #2) for
    when a persisted slug no longer exists in the repo
    (workload was renamed or deleted) and is blocking
    subsequent deploys via exclude_unknown_slug. Without
    this endpoint the only option was psql + hand-DELETE;
    the CLI's `gregale deployments exclude clear
    --slug=NAME --project-slug=SLUG` calls into here as the
    operator-grade path. Idempotent — DELETE on no row
    returns 404 scope_exclusion_not_found so the CLI can
    render \"already clear\" without surfacing a hard error.

    Args:
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeleteDeploymentScopeExclusionResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        slug2=slug2,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeleteDeploymentScopeExclusionResponse200 | Problem | None:
    r"""Drop a persisted --exclude row from deployment_scope_exclusions.

     Operator escape hatch (ADR-124 code-review fix #2) for
    when a persisted slug no longer exists in the repo
    (workload was renamed or deleted) and is blocking
    subsequent deploys via exclude_unknown_slug. Without
    this endpoint the only option was psql + hand-DELETE;
    the CLI's `gregale deployments exclude clear
    --slug=NAME --project-slug=SLUG` calls into here as the
    operator-grade path. Idempotent — DELETE on no row
    returns 404 scope_exclusion_not_found so the CLI can
    render \"already clear\" without surfacing a hard error.

    Args:
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeleteDeploymentScopeExclusionResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        slug2=slug2,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeleteDeploymentScopeExclusionResponse200 | Problem]:
    r"""Drop a persisted --exclude row from deployment_scope_exclusions.

     Operator escape hatch (ADR-124 code-review fix #2) for
    when a persisted slug no longer exists in the repo
    (workload was renamed or deleted) and is blocking
    subsequent deploys via exclude_unknown_slug. Without
    this endpoint the only option was psql + hand-DELETE;
    the CLI's `gregale deployments exclude clear
    --slug=NAME --project-slug=SLUG` calls into here as the
    operator-grade path. Idempotent — DELETE on no row
    returns 404 scope_exclusion_not_found so the CLI can
    render \"already clear\" without surfacing a hard error.

    Args:
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeleteDeploymentScopeExclusionResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        slug2=slug2,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeleteDeploymentScopeExclusionResponse200 | Problem | None:
    r"""Drop a persisted --exclude row from deployment_scope_exclusions.

     Operator escape hatch (ADR-124 code-review fix #2) for
    when a persisted slug no longer exists in the repo
    (workload was renamed or deleted) and is blocking
    subsequent deploys via exclude_unknown_slug. Without
    this endpoint the only option was psql + hand-DELETE;
    the CLI's `gregale deployments exclude clear
    --slug=NAME --project-slug=SLUG` calls into here as the
    operator-grade path. Idempotent — DELETE on no row
    returns 404 scope_exclusion_not_found so the CLI can
    render \"already clear\" without surfacing a hard error.

    Args:
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeleteDeploymentScopeExclusionResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            slug2=slug2,
            client=client,
        )
    ).parsed
