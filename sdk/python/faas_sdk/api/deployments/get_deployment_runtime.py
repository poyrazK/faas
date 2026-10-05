from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_runtime_response import DeploymentRuntimeResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/runtime".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentRuntimeResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentRuntimeResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentRuntimeResponse | Problem]:
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
) -> Response[DeploymentRuntimeResponse | Problem]:
    """Inspect immutable runtime base identity.

     Read-only artifact evidence for Gregale-managed function runtimes (ADR-596).
    Older artifacts return unknown rather than inferring their base from builder
    provenance. Published candidates are limited to the same family and architecture,
    at most 50 newest publications. Publication does not establish upgrade compatibility.
    No VM is booted and no deployment, traffic or environment setting is changed.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentRuntimeResponse | Problem]
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
) -> DeploymentRuntimeResponse | Problem | None:
    """Inspect immutable runtime base identity.

     Read-only artifact evidence for Gregale-managed function runtimes (ADR-596).
    Older artifacts return unknown rather than inferring their base from builder
    provenance. Published candidates are limited to the same family and architecture,
    at most 50 newest publications. Publication does not establish upgrade compatibility.
    No VM is booted and no deployment, traffic or environment setting is changed.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentRuntimeResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentRuntimeResponse | Problem]:
    """Inspect immutable runtime base identity.

     Read-only artifact evidence for Gregale-managed function runtimes (ADR-596).
    Older artifacts return unknown rather than inferring their base from builder
    provenance. Published candidates are limited to the same family and architecture,
    at most 50 newest publications. Publication does not establish upgrade compatibility.
    No VM is booted and no deployment, traffic or environment setting is changed.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentRuntimeResponse | Problem]
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
) -> DeploymentRuntimeResponse | Problem | None:
    """Inspect immutable runtime base identity.

     Read-only artifact evidence for Gregale-managed function runtimes (ADR-596).
    Older artifacts return unknown rather than inferring their base from builder
    provenance. Published candidates are limited to the same family and architecture,
    at most 50 newest publications. Publication does not establish upgrade compatibility.
    No VM is booted and no deployment, traffic or environment setting is changed.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentRuntimeResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
