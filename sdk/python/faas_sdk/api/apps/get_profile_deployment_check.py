from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.profile_deployment_check import ProfileDeploymentCheck
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/profiles/deployment-checks/{id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileDeploymentCheck | None:
    if response.status_code == 200:
        response_200 = ProfileDeploymentCheck.from_dict(response.json())

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
) -> Response[Problem | ProfileDeploymentCheck]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProfileDeploymentCheck]:
    """Read the CPU-check receipt for a deployment.

     Reads an owned app's receipt by deployment UUID with app-read access. Missing or foreign receipts
    return not found. A saved-investigation link restores the differential flamegraph; if the
    investigation quota was full or the saved record was deleted, the link keeps the original comparison
    selections instead. Missing baseline comparisons have no link.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileDeploymentCheck]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProfileDeploymentCheck | None:
    """Read the CPU-check receipt for a deployment.

     Reads an owned app's receipt by deployment UUID with app-read access. Missing or foreign receipts
    return not found. A saved-investigation link restores the differential flamegraph; if the
    investigation quota was full or the saved record was deleted, the link keeps the original comparison
    selections instead. Missing baseline comparisons have no link.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileDeploymentCheck
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProfileDeploymentCheck]:
    """Read the CPU-check receipt for a deployment.

     Reads an owned app's receipt by deployment UUID with app-read access. Missing or foreign receipts
    return not found. A saved-investigation link restores the differential flamegraph; if the
    investigation quota was full or the saved record was deleted, the link keeps the original comparison
    selections instead. Missing baseline comparisons have no link.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileDeploymentCheck]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProfileDeploymentCheck | None:
    """Read the CPU-check receipt for a deployment.

     Reads an owned app's receipt by deployment UUID with app-read access. Missing or foreign receipts
    return not found. A saved-investigation link restores the differential flamegraph; if the
    investigation quota was full or the saved record was deleted, the link keeps the original comparison
    selections instead. Missing baseline comparisons have no link.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileDeploymentCheck
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
