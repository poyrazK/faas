from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_project_delete_deployment_scope_exclusion_response_200 import (
    OrgProjectDeleteDeploymentScopeExclusionResponse200,
)
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    slug2: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/orgs/{organization}/projects/{slug}/exclusions/{slug2}".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            slug2=quote(str(slug2), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = OrgProjectDeleteDeploymentScopeExclusionResponse200.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem]:
    """Remove a persisted company project deployment exclusion.

     Removes a company workload exclusion saved by a previous persist-exclude deployment so a later
    source apply can include it again.

    Args:
        organization (str):
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        slug2=slug2,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem | None:
    """Remove a persisted company project deployment exclusion.

     Removes a company workload exclusion saved by a previous persist-exclude deployment so a later
    source apply can include it again.

    Args:
        organization (str):
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        slug2=slug2,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem]:
    """Remove a persisted company project deployment exclusion.

     Removes a company workload exclusion saved by a previous persist-exclude deployment so a later
    source apply can include it again.

    Args:
        organization (str):
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        slug2=slug2,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    slug2: str,
    *,
    client: AuthenticatedClient | Client,
) -> OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem | None:
    """Remove a persisted company project deployment exclusion.

     Removes a company workload exclusion saved by a previous persist-exclude deployment so a later
    source apply can include it again.

    Args:
        organization (str):
        slug (str):
        slug2 (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgProjectDeleteDeploymentScopeExclusionResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            slug2=slug2,
            client=client,
        )
    ).parsed
