from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_receiver_invite import APISandboxReceiverInvite
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandbox-runs/{run}/receiver-invite".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxReceiverInvite | Problem:
    if response.status_code == 200:
        response_200 = APISandboxReceiverInvite.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxReceiverInvite | Problem]:
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
) -> Response[APISandboxReceiverInvite | Problem]:
    """Read the receiver setup status.

     Read readiness for this release without returning any credentials.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxReceiverInvite | Problem]
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
) -> APISandboxReceiverInvite | Problem | None:
    """Read the receiver setup status.

     Read readiness for this release without returning any credentials.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxReceiverInvite | Problem
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
) -> Response[APISandboxReceiverInvite | Problem]:
    """Read the receiver setup status.

     Read readiness for this release without returning any credentials.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxReceiverInvite | Problem]
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
) -> APISandboxReceiverInvite | Problem | None:
    """Read the receiver setup status.

     Read readiness for this release without returning any credentials.

    Args:
        slug (str):
        deployment (UUID):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxReceiverInvite | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            run=run,
            client=client,
        )
    ).parsed
