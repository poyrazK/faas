from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_approval_status_response import ProjectEnvironmentApprovalStatusResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    approval: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/approvals/{approval}".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            approval=quote(str(approval), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentApprovalStatusResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentApprovalStatusResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectEnvironmentApprovalStatusResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    approval: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectEnvironmentApprovalStatusResponse]:
    """Get the lifecycle status of a project environment approval.

     The raw approval token is never returned by this endpoint.

    Args:
        slug (str):
        environment (str):
        approval (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentApprovalStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        approval=approval,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    approval: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectEnvironmentApprovalStatusResponse | None:
    """Get the lifecycle status of a project environment approval.

     The raw approval token is never returned by this endpoint.

    Args:
        slug (str):
        environment (str):
        approval (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentApprovalStatusResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        approval=approval,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    approval: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectEnvironmentApprovalStatusResponse]:
    """Get the lifecycle status of a project environment approval.

     The raw approval token is never returned by this endpoint.

    Args:
        slug (str):
        environment (str):
        approval (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentApprovalStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        approval=approval,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    approval: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectEnvironmentApprovalStatusResponse | None:
    """Get the lifecycle status of a project environment approval.

     The raw approval token is never returned by this endpoint.

    Args:
        slug (str):
        environment (str):
        approval (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentApprovalStatusResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            approval=approval,
            client=client,
        )
    ).parsed
