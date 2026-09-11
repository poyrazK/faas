from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.git_hub_install_mutation_request import GitHubInstallMutationRequest
from ...models.git_hub_install_status import GitHubInstallStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: GitHubInstallMutationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/install/sync".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GitHubInstallStatus | Problem | None:
    if response.status_code == 200:
        response_200 = GitHubInstallStatus.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GitHubInstallStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: GitHubInstallMutationRequest,
) -> Response[GitHubInstallStatus | Problem]:
    """Reconcile an app's GitHub repository access immediately.

     Cookie-session-authenticated and CSRF-protected. Queries GitHub's
    current installation repository list and detaches the app only when
    its bound repository is no longer accessible. The response includes
    the remote repository count and whether this app was detached.

    Args:
        slug (str):
        body (GitHubInstallMutationRequest): Double-submit CSRF envelope for customer-facing
            GitHub connection
            mutations. The token is returned by GET /v1/apps/{slug}/install or
            GET /v1/apps/{slug}/install/bind and must match the named
            `faas_csrf_github_install` cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GitHubInstallStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: GitHubInstallMutationRequest,
) -> GitHubInstallStatus | Problem | None:
    """Reconcile an app's GitHub repository access immediately.

     Cookie-session-authenticated and CSRF-protected. Queries GitHub's
    current installation repository list and detaches the app only when
    its bound repository is no longer accessible. The response includes
    the remote repository count and whether this app was detached.

    Args:
        slug (str):
        body (GitHubInstallMutationRequest): Double-submit CSRF envelope for customer-facing
            GitHub connection
            mutations. The token is returned by GET /v1/apps/{slug}/install or
            GET /v1/apps/{slug}/install/bind and must match the named
            `faas_csrf_github_install` cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GitHubInstallStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: GitHubInstallMutationRequest,
) -> Response[GitHubInstallStatus | Problem]:
    """Reconcile an app's GitHub repository access immediately.

     Cookie-session-authenticated and CSRF-protected. Queries GitHub's
    current installation repository list and detaches the app only when
    its bound repository is no longer accessible. The response includes
    the remote repository count and whether this app was detached.

    Args:
        slug (str):
        body (GitHubInstallMutationRequest): Double-submit CSRF envelope for customer-facing
            GitHub connection
            mutations. The token is returned by GET /v1/apps/{slug}/install or
            GET /v1/apps/{slug}/install/bind and must match the named
            `faas_csrf_github_install` cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GitHubInstallStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: GitHubInstallMutationRequest,
) -> GitHubInstallStatus | Problem | None:
    """Reconcile an app's GitHub repository access immediately.

     Cookie-session-authenticated and CSRF-protected. Queries GitHub's
    current installation repository list and detaches the app only when
    its bound repository is no longer accessible. The response includes
    the remote repository count and whether this app was detached.

    Args:
        slug (str):
        body (GitHubInstallMutationRequest): Double-submit CSRF envelope for customer-facing
            GitHub connection
            mutations. The token is returned by GET /v1/apps/{slug}/install or
            GET /v1/apps/{slug}/install/bind and must match the named
            `faas_csrf_github_install` cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GitHubInstallStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
