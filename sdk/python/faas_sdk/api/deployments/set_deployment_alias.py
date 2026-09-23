from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_alias_response import DeploymentAliasResponse
from ...models.problem import Problem
from ...models.set_deployment_alias_request import SetDeploymentAliasRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: SetDeploymentAliasRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployment-aliases/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentAliasResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentAliasResponse.from_dict(response.json())

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
) -> Response[DeploymentAliasResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: SetDeploymentAliasRequest,
) -> Response[DeploymentAliasResponse | Problem]:
    """Point a named alias at an immutable deployment.

     The target must be a routable deployment that belongs to the app. This updates only the alias
    mapping; it does not shift production traffic. The alias name and app identifier must fit together
    in one DNS label.

    Args:
        slug (str):
        name (str):
        body (SetDeploymentAliasRequest): Request to point one named alias at an exact immutable
            deployment row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentAliasResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: SetDeploymentAliasRequest,
) -> DeploymentAliasResponse | Problem | None:
    """Point a named alias at an immutable deployment.

     The target must be a routable deployment that belongs to the app. This updates only the alias
    mapping; it does not shift production traffic. The alias name and app identifier must fit together
    in one DNS label.

    Args:
        slug (str):
        name (str):
        body (SetDeploymentAliasRequest): Request to point one named alias at an exact immutable
            deployment row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentAliasResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: SetDeploymentAliasRequest,
) -> Response[DeploymentAliasResponse | Problem]:
    """Point a named alias at an immutable deployment.

     The target must be a routable deployment that belongs to the app. This updates only the alias
    mapping; it does not shift production traffic. The alias name and app identifier must fit together
    in one DNS label.

    Args:
        slug (str):
        name (str):
        body (SetDeploymentAliasRequest): Request to point one named alias at an exact immutable
            deployment row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentAliasResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: SetDeploymentAliasRequest,
) -> DeploymentAliasResponse | Problem | None:
    """Point a named alias at an immutable deployment.

     The target must be a routable deployment that belongs to the app. This updates only the alias
    mapping; it does not shift production traffic. The alias name and app identifier must fit together
    in one DNS label.

    Args:
        slug (str):
        name (str):
        body (SetDeploymentAliasRequest): Request to point one named alias at an exact immutable
            deployment row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentAliasResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
