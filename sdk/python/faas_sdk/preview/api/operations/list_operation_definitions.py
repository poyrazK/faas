from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_definitions_response import OperationDefinitionsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployments/{deployment_id}/operation-definitions".format(
            slug=quote(str(slug), safe=""),
            deployment_id=quote(str(deployment_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationDefinitionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationDefinitionsResponse.from_dict(response.json())

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
) -> Response[OperationDefinitionsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OperationDefinitionsResponse | Problem]:
    """List immutable Operations contracts for one owned deployment.

     Requires account read scope and MFA. Deployment identity selects the environment and pins; reads
    remain available while admission is closed. No mutable active-deployment selector is inferred.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationDefinitionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OperationDefinitionsResponse | Problem | None:
    """List immutable Operations contracts for one owned deployment.

     Requires account read scope and MFA. Deployment identity selects the environment and pins; reads
    remain available while admission is closed. No mutable active-deployment selector is inferred.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationDefinitionsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment_id=deployment_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OperationDefinitionsResponse | Problem]:
    """List immutable Operations contracts for one owned deployment.

     Requires account read scope and MFA. Deployment identity selects the environment and pins; reads
    remain available while admission is closed. No mutable active-deployment selector is inferred.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationDefinitionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OperationDefinitionsResponse | Problem | None:
    """List immutable Operations contracts for one owned deployment.

     Requires account read scope and MFA. Deployment identity selects the environment and pins; reads
    remain available while admission is closed. No mutable active-deployment selector is inferred.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationDefinitionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment_id=deployment_id,
            client=client,
        )
    ).parsed
