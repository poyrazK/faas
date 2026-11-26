from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_run_share_response import APISandboxRunShareResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandbox-runs/{run}/report-link".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxRunShareResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APISandboxRunShareResponse.from_dict(response.json())

        return response_201

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxRunShareResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APISandboxRunShareResponse | Problem]:
    """Issue a read-only token for a finished scenario run report.

     A new link replaces the previous link for this run. The token expires after 24 hours.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxRunShareResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        run=run,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APISandboxRunShareResponse | Problem | None:
    """Issue a read-only token for a finished scenario run report.

     A new link replaces the previous link for this run. The token expires after 24 hours.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxRunShareResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APISandboxRunShareResponse | Problem]:
    """Issue a read-only token for a finished scenario run report.

     A new link replaces the previous link for this run. The token expires after 24 hours.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxRunShareResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        run=run,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APISandboxRunShareResponse | Problem | None:
    """Issue a read-only token for a finished scenario run report.

     A new link replaces the previous link for this run. The token expires after 24 hours.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxRunShareResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            run=run,
            client=client,
        )
    ).parsed
