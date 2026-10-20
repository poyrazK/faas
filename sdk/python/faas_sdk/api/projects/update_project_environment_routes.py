from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_route_policy_response import ProjectEnvironmentRoutePolicyResponse
from ...models.update_project_environment_route_policy_request import UpdateProjectEnvironmentRoutePolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    workload: str,
    *,
    body: UpdateProjectEnvironmentRoutePolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/projects/{slug}/environments/{environment}/workloads/{workload}/routes".format(
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
) -> Problem | ProjectEnvironmentRoutePolicyResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentRoutePolicyResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectEnvironmentRoutePolicyResponse]:
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
    body: UpdateProjectEnvironmentRoutePolicyRequest,
) -> Response[Problem | ProjectEnvironmentRoutePolicyResponse]:
    """Replace a workload's declared-route contract in one environment.

     Scoped deployment URLs use this contract; the ordinary application hostname retains its application-
    wide contract.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutePolicyRequest): Complete replacement for a workload's
            environment route contract. Enabling enforcement requires a non-empty explicit route list;
            application-wide OpenAPI documents are not cloned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentRoutePolicyResponse]
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
    body: UpdateProjectEnvironmentRoutePolicyRequest,
) -> Problem | ProjectEnvironmentRoutePolicyResponse | None:
    """Replace a workload's declared-route contract in one environment.

     Scoped deployment URLs use this contract; the ordinary application hostname retains its application-
    wide contract.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutePolicyRequest): Complete replacement for a workload's
            environment route contract. Enabling enforcement requires a non-empty explicit route list;
            application-wide OpenAPI documents are not cloned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentRoutePolicyResponse
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
    body: UpdateProjectEnvironmentRoutePolicyRequest,
) -> Response[Problem | ProjectEnvironmentRoutePolicyResponse]:
    """Replace a workload's declared-route contract in one environment.

     Scoped deployment URLs use this contract; the ordinary application hostname retains its application-
    wide contract.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutePolicyRequest): Complete replacement for a workload's
            environment route contract. Enabling enforcement requires a non-empty explicit route list;
            application-wide OpenAPI documents are not cloned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentRoutePolicyResponse]
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
    body: UpdateProjectEnvironmentRoutePolicyRequest,
) -> Problem | ProjectEnvironmentRoutePolicyResponse | None:
    """Replace a workload's declared-route contract in one environment.

     Scoped deployment URLs use this contract; the ordinary application hostname retains its application-
    wide contract.

    Args:
        slug (str):
        environment (str):
        workload (str):
        body (UpdateProjectEnvironmentRoutePolicyRequest): Complete replacement for a workload's
            environment route contract. Enabling enforcement requires a non-empty explicit route list;
            application-wide OpenAPI documents are not cloned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentRoutePolicyResponse
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
