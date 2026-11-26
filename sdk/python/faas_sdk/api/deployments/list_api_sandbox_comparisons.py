from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_comparisons_response import APISandboxComparisonsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandbox-comparisons".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxComparisonsResponse | Problem:
    if response.status_code == 200:
        response_200 = APISandboxComparisonsResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxComparisonsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APISandboxComparisonsResponse | Problem]:
    """List recent sandbox release comparisons and previous eligible baselines.

     The candidate comes from the deployment path. Baselines are retained releases of the same
    application, most recent first.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxComparisonsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APISandboxComparisonsResponse | Problem | None:
    """List recent sandbox release comparisons and previous eligible baselines.

     The candidate comes from the deployment path. Baselines are retained releases of the same
    application, most recent first.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxComparisonsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APISandboxComparisonsResponse | Problem]:
    """List recent sandbox release comparisons and previous eligible baselines.

     The candidate comes from the deployment path. Baselines are retained releases of the same
    application, most recent first.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxComparisonsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APISandboxComparisonsResponse | Problem | None:
    """List recent sandbox release comparisons and previous eligible baselines.

     The candidate comes from the deployment path. Baselines are retained releases of the same
    application, most recent first.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxComparisonsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
        )
    ).parsed
