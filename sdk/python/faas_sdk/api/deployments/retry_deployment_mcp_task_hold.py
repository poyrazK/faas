from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/mcp-task-hold/retry".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
    if response.status_code == 202:
        response_202 = DeploymentResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentResponse | Problem]:
    """Schedule another recovery attempt for an active MCP compatibility hold.

     Requires deployment write scope and MFA policy. Returns 202 when recovery is already due or a new
    retry is scheduled. An active lease is preserved and schedules a follow-up. The worker repeats
    compatibility, readiness, smoke and lifecycle checks; this action does not grant a migration
    override.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentResponse | Problem | None:
    """Schedule another recovery attempt for an active MCP compatibility hold.

     Requires deployment write scope and MFA policy. Returns 202 when recovery is already due or a new
    retry is scheduled. An active lease is preserved and schedules a follow-up. The worker repeats
    compatibility, readiness, smoke and lifecycle checks; this action does not grant a migration
    override.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentResponse | Problem]:
    """Schedule another recovery attempt for an active MCP compatibility hold.

     Requires deployment write scope and MFA policy. Returns 202 when recovery is already due or a new
    retry is scheduled. An active lease is preserved and schedules a follow-up. The worker repeats
    compatibility, readiness, smoke and lifecycle checks; this action does not grant a migration
    override.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentResponse | Problem | None:
    """Schedule another recovery attempt for an active MCP compatibility hold.

     Requires deployment write scope and MFA policy. Returns 202 when recovery is already due or a new
    retry is scheduled. An active lease is preserved and schedules a follow-up. The worker repeats
    compatibility, readiness, smoke and lifecycle checks; this action does not grant a migration
    override.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
