from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_route_policy_snapshot_response import DeploymentRoutePolicySnapshotResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployments/{deployment}/route-policy".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentRoutePolicySnapshotResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentRoutePolicySnapshotResponse.from_dict(response.json())

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
) -> Response[DeploymentRoutePolicySnapshotResponse | Problem]:
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
) -> Response[DeploymentRoutePolicySnapshotResponse | Problem]:
    """Read the gateway policy captured with a deployment.

     Returns the immutable edge-rule set captured atomically when this
    deployment first became live. Rules use the same owner-scoped shape as
    the current app edge-rule endpoint. Older deployments without a
    snapshot return 404 so callers can report historical policy as unknown.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentRoutePolicySnapshotResponse | Problem]
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
) -> DeploymentRoutePolicySnapshotResponse | Problem | None:
    """Read the gateway policy captured with a deployment.

     Returns the immutable edge-rule set captured atomically when this
    deployment first became live. Rules use the same owner-scoped shape as
    the current app edge-rule endpoint. Older deployments without a
    snapshot return 404 so callers can report historical policy as unknown.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentRoutePolicySnapshotResponse | Problem
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
) -> Response[DeploymentRoutePolicySnapshotResponse | Problem]:
    """Read the gateway policy captured with a deployment.

     Returns the immutable edge-rule set captured atomically when this
    deployment first became live. Rules use the same owner-scoped shape as
    the current app edge-rule endpoint. Older deployments without a
    snapshot return 404 so callers can report historical policy as unknown.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentRoutePolicySnapshotResponse | Problem]
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
) -> DeploymentRoutePolicySnapshotResponse | Problem | None:
    """Read the gateway policy captured with a deployment.

     Returns the immutable edge-rule set captured atomically when this
    deployment first became live. Rules use the same owner-scoped shape as
    the current app edge-rule endpoint. Older deployments without a
    snapshot return 404 so callers can report historical policy as unknown.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentRoutePolicySnapshotResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
        )
    ).parsed
