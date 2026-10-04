from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_git_source import EnvironmentGitSource
from ...models.environment_git_source_update import EnvironmentGitSourceUpdate
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: EnvironmentGitSourceUpdate,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/projects/{slug}/environments/{environment}/gitops/source".format(
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
) -> EnvironmentGitSource | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentGitSource.from_dict(response.json())

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
) -> Response[EnvironmentGitSource | Problem]:
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
    body: EnvironmentGitSourceUpdate,
    idempotency_key: str | Unset = UNSET,
) -> Response[EnvironmentGitSource | Problem]:
    """Update report, enforce, pruning, and suspension controls.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str | Unset):
        body (EnvironmentGitSourceUpdate): Change controls with a generation fence; suspended
            sources keep ownership.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentGitSource | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
        idempotency_key=idempotency_key,
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
    body: EnvironmentGitSourceUpdate,
    idempotency_key: str | Unset = UNSET,
) -> EnvironmentGitSource | Problem | None:
    """Update report, enforce, pruning, and suspension controls.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str | Unset):
        body (EnvironmentGitSourceUpdate): Change controls with a generation fence; suspended
            sources keep ownership.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentGitSource | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnvironmentGitSourceUpdate,
    idempotency_key: str | Unset = UNSET,
) -> Response[EnvironmentGitSource | Problem]:
    """Update report, enforce, pruning, and suspension controls.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str | Unset):
        body (EnvironmentGitSourceUpdate): Change controls with a generation fence; suspended
            sources keep ownership.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentGitSource | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnvironmentGitSourceUpdate,
    idempotency_key: str | Unset = UNSET,
) -> EnvironmentGitSource | Problem | None:
    """Update report, enforce, pruning, and suspension controls.

    Args:
        slug (str):
        environment (str):
        idempotency_key (str | Unset):
        body (EnvironmentGitSourceUpdate): Change controls with a generation fence; suspended
            sources keep ownership.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentGitSource | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
