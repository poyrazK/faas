from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_edge_policy_response import ProjectEnvironmentEdgePolicyResponse
from ...models.update_project_environment_routing_policy_request import UpdateProjectEnvironmentRoutingPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    workload: str,
    *,
    body: UpdateProjectEnvironmentRoutingPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/projects/{slug}/environments/{environment}/workloads/{workload}/routing-policies".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            workload=quote(str(workload), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentEdgePolicyResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentEdgePolicyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[Problem | ProjectEnvironmentEdgePolicyResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateProjectEnvironmentRoutingPolicyRequest,
) -> Response[Problem | ProjectEnvironmentEdgePolicyResponse]:
    """Replace redirect and rewrite rules for a workload in one environment.

     An explicit empty list disables inherited redirects and rewrites on the stable environment URL.
    Headers, CORS, other rule kinds, and ordinary application hosts are unchanged.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutingPolicyRequest): Complete replacement for environment
            redirect/rewrite rules, independently of headers/CORS rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentEdgePolicyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        workload=workload,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateProjectEnvironmentRoutingPolicyRequest,
) -> Problem | ProjectEnvironmentEdgePolicyResponse | None:
    """Replace redirect and rewrite rules for a workload in one environment.

     An explicit empty list disables inherited redirects and rewrites on the stable environment URL.
    Headers, CORS, other rule kinds, and ordinary application hosts are unchanged.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutingPolicyRequest): Complete replacement for environment
            redirect/rewrite rules, independently of headers/CORS rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentEdgePolicyResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        workload=workload,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateProjectEnvironmentRoutingPolicyRequest,
) -> Response[Problem | ProjectEnvironmentEdgePolicyResponse]:
    """Replace redirect and rewrite rules for a workload in one environment.

     An explicit empty list disables inherited redirects and rewrites on the stable environment URL.
    Headers, CORS, other rule kinds, and ordinary application hosts are unchanged.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutingPolicyRequest): Complete replacement for environment
            redirect/rewrite rules, independently of headers/CORS rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentEdgePolicyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        workload=workload,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateProjectEnvironmentRoutingPolicyRequest,
) -> Problem | ProjectEnvironmentEdgePolicyResponse | None:
    """Replace redirect and rewrite rules for a workload in one environment.

     An explicit empty list disables inherited redirects and rewrites on the stable environment URL.
    Headers, CORS, other rule kinds, and ordinary application hosts are unchanged.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutingPolicyRequest): Complete replacement for environment
            redirect/rewrite rules, independently of headers/CORS rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentEdgePolicyResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            workload=workload,
            client=client,
            body=body,
        )
    ).parsed
