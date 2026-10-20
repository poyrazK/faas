from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_project_environment_approval_request import CreateProjectEnvironmentApprovalRequest
from ...models.problem import Problem
from ...models.project_environment_approval_response import ProjectEnvironmentApprovalResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: CreateProjectEnvironmentApprovalRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/approvals".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentApprovalResponse | None:
    if response.status_code == 201:
        response_201 = ProjectEnvironmentApprovalResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectEnvironmentApprovalResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectEnvironmentApprovalRequest,
) -> Response[Problem | ProjectEnvironmentApprovalResponse]:
    """Approve one exact plan for a protected project environment.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentApprovalRequest): Request to approve one exact plan for a
            protected environment. Provide exactly one of plan_token or promotion_token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentApprovalResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectEnvironmentApprovalRequest,
) -> Problem | ProjectEnvironmentApprovalResponse | None:
    """Approve one exact plan for a protected project environment.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentApprovalRequest): Request to approve one exact plan for a
            protected environment. Provide exactly one of plan_token or promotion_token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentApprovalResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectEnvironmentApprovalRequest,
) -> Response[Problem | ProjectEnvironmentApprovalResponse]:
    """Approve one exact plan for a protected project environment.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentApprovalRequest): Request to approve one exact plan for a
            protected environment. Provide exactly one of plan_token or promotion_token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentApprovalResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectEnvironmentApprovalRequest,
) -> Problem | ProjectEnvironmentApprovalResponse | None:
    """Approve one exact plan for a protected project environment.

    Args:
        slug (str):
        environment (str):
        body (CreateProjectEnvironmentApprovalRequest): Request to approve one exact plan for a
            protected environment. Provide exactly one of plan_token or promotion_token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentApprovalResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
