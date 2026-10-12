from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_companions_response import DeploymentCompanionsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/companions".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentCompanionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentCompanionsResponse.from_dict(response.json())

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
) -> Response[DeploymentCompanionsResponse | Problem]:
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
) -> Response[DeploymentCompanionsResponse | Problem]:
    """Inspect owned deployment companion declarations.

     Returns bounded declared configuration from this exact deployment.
    The deployment identifies its environment scope; query scope is not
    supported. Empty declarations are distinct from unavailable retained
    metadata. No environment values, ciphertext, secret references or
    commands are returned. No workload is probed and no execution health
    is inferred; absent telemetry is Unverified. Managed preset availability
    requires separate runtime qualification and does not imply that all
    custom helpers are unavailable. Requires deployment-read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentCompanionsResponse | Problem]
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
) -> DeploymentCompanionsResponse | Problem | None:
    """Inspect owned deployment companion declarations.

     Returns bounded declared configuration from this exact deployment.
    The deployment identifies its environment scope; query scope is not
    supported. Empty declarations are distinct from unavailable retained
    metadata. No environment values, ciphertext, secret references or
    commands are returned. No workload is probed and no execution health
    is inferred; absent telemetry is Unverified. Managed preset availability
    requires separate runtime qualification and does not imply that all
    custom helpers are unavailable. Requires deployment-read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentCompanionsResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentCompanionsResponse | Problem]:
    """Inspect owned deployment companion declarations.

     Returns bounded declared configuration from this exact deployment.
    The deployment identifies its environment scope; query scope is not
    supported. Empty declarations are distinct from unavailable retained
    metadata. No environment values, ciphertext, secret references or
    commands are returned. No workload is probed and no execution health
    is inferred; absent telemetry is Unverified. Managed preset availability
    requires separate runtime qualification and does not imply that all
    custom helpers are unavailable. Requires deployment-read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentCompanionsResponse | Problem]
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
) -> DeploymentCompanionsResponse | Problem | None:
    """Inspect owned deployment companion declarations.

     Returns bounded declared configuration from this exact deployment.
    The deployment identifies its environment scope; query scope is not
    supported. Empty declarations are distinct from unavailable retained
    metadata. No environment values, ciphertext, secret references or
    commands are returned. No workload is probed and no execution health
    is inferred; absent telemetry is Unverified. Managed preset availability
    requires separate runtime qualification and does not imply that all
    custom helpers are unavailable. Requires deployment-read scope and MFA.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentCompanionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
