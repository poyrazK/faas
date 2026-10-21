from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_deployment_check_request import ProjectDeploymentCheckRequest
from ...models.project_deployment_readiness import ProjectDeploymentReadiness
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    *,
    body: ProjectDeploymentCheckRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/projects/{slug}/deployment-readiness".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectDeploymentReadiness | None:
    if response.status_code == 200:
        response_200 = ProjectDeploymentReadiness.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectDeploymentReadiness]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectDeploymentCheckRequest,
) -> Response[Problem | ProjectDeploymentReadiness]:
    """Check company project deployment or promotion readiness.

     Company form of the readiness check. A project the company does not have yet can be checked as a
    proposed first deployment; a slug used outside the company returns 409. Reads only.

    Args:
        organization (str):
        slug (str):
        body (ProjectDeploymentCheckRequest): Metadata-only project intent. Local source checks
            provide workloads; promotion checks provide from_environment instead. Proposed files never
            stage production changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectDeploymentReadiness]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectDeploymentCheckRequest,
) -> Problem | ProjectDeploymentReadiness | None:
    """Check company project deployment or promotion readiness.

     Company form of the readiness check. A project the company does not have yet can be checked as a
    proposed first deployment; a slug used outside the company returns 409. Reads only.

    Args:
        organization (str):
        slug (str):
        body (ProjectDeploymentCheckRequest): Metadata-only project intent. Local source checks
            provide workloads; promotion checks provide from_environment instead. Proposed files never
            stage production changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectDeploymentReadiness
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectDeploymentCheckRequest,
) -> Response[Problem | ProjectDeploymentReadiness]:
    """Check company project deployment or promotion readiness.

     Company form of the readiness check. A project the company does not have yet can be checked as a
    proposed first deployment; a slug used outside the company returns 409. Reads only.

    Args:
        organization (str):
        slug (str):
        body (ProjectDeploymentCheckRequest): Metadata-only project intent. Local source checks
            provide workloads; promotion checks provide from_environment instead. Proposed files never
            stage production changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectDeploymentReadiness]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectDeploymentCheckRequest,
) -> Problem | ProjectDeploymentReadiness | None:
    """Check company project deployment or promotion readiness.

     Company form of the readiness check. A project the company does not have yet can be checked as a
    proposed first deployment; a slug used outside the company returns 409. Reads only.

    Args:
        organization (str):
        slug (str):
        body (ProjectDeploymentCheckRequest): Metadata-only project intent. Local source checks
            provide workloads; promotion checks provide from_environment instead. Proposed files never
            stage production changes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectDeploymentReadiness
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
