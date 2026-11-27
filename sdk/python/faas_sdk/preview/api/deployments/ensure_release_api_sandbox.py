from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_create_request import APISandboxCreateRequest
from ...models.api_sandbox_response import APISandboxResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    body: APISandboxCreateRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandboxes/{sandbox}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            sandbox=quote(str(sandbox), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxResponse.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = APISandboxResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxCreateRequest | Unset = UNSET,
) -> Response[APISandboxResponse | Problem]:
    """Queue or retrieve an API sandbox.

     Starts background provisioning of the selected artifact and an isolated PostgreSQL database. Reuse
    the sandbox UUID to get the existing sandbox after an interrupted response.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):
        body (APISandboxCreateRequest | Unset): Configuration for a fresh copy of an API release.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxCreateRequest | Unset = UNSET,
) -> APISandboxResponse | Problem | None:
    """Queue or retrieve an API sandbox.

     Starts background provisioning of the selected artifact and an isolated PostgreSQL database. Reuse
    the sandbox UUID to get the existing sandbox after an interrupted response.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):
        body (APISandboxCreateRequest | Unset): Configuration for a fresh copy of an API release.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxCreateRequest | Unset = UNSET,
) -> Response[APISandboxResponse | Problem]:
    """Queue or retrieve an API sandbox.

     Starts background provisioning of the selected artifact and an isolated PostgreSQL database. Reuse
    the sandbox UUID to get the existing sandbox after an interrupted response.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):
        body (APISandboxCreateRequest | Unset): Configuration for a fresh copy of an API release.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxCreateRequest | Unset = UNSET,
) -> APISandboxResponse | Problem | None:
    """Queue or retrieve an API sandbox.

     Starts background provisioning of the selected artifact and an isolated PostgreSQL database. Reuse
    the sandbox UUID to get the existing sandbox after an interrupted response.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):
        body (APISandboxCreateRequest | Unset): Configuration for a fresh copy of an API release.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            sandbox=sandbox,
            client=client,
            body=body,
        )
    ).parsed
