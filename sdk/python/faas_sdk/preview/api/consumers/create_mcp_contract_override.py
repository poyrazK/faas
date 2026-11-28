from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_mcp_contract_override_request import CreateMCPContractOverrideRequest
from ...models.mcp_contract_override import MCPContractOverride
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateMCPContractOverrideRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mcp-contract/overrides".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MCPContractOverride | Problem | None:
    if response.status_code == 201:
        response_201 = MCPContractOverride.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MCPContractOverride | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPContractOverrideRequest,
) -> Response[MCPContractOverride | Problem]:
    """Let the next breaking production deploy of a listed MCP server ship.

     Records why paying customers' tools may break. The override admits
    one breaking or unverified candidate before it expires (one hour by
    default, at most 24 hours); an app holds at most five open
    overrides. The webhook event mcp.contract.changed and the listing's
    changelog tell customers about the change once it is live.

    Args:
        slug (str):
        body (CreateMCPContractOverrideRequest): Why the next breaking candidate may ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MCPContractOverride | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPContractOverrideRequest,
) -> MCPContractOverride | Problem | None:
    """Let the next breaking production deploy of a listed MCP server ship.

     Records why paying customers' tools may break. The override admits
    one breaking or unverified candidate before it expires (one hour by
    default, at most 24 hours); an app holds at most five open
    overrides. The webhook event mcp.contract.changed and the listing's
    changelog tell customers about the change once it is live.

    Args:
        slug (str):
        body (CreateMCPContractOverrideRequest): Why the next breaking candidate may ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MCPContractOverride | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPContractOverrideRequest,
) -> Response[MCPContractOverride | Problem]:
    """Let the next breaking production deploy of a listed MCP server ship.

     Records why paying customers' tools may break. The override admits
    one breaking or unverified candidate before it expires (one hour by
    default, at most 24 hours); an app holds at most five open
    overrides. The webhook event mcp.contract.changed and the listing's
    changelog tell customers about the change once it is live.

    Args:
        slug (str):
        body (CreateMCPContractOverrideRequest): Why the next breaking candidate may ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MCPContractOverride | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPContractOverrideRequest,
) -> MCPContractOverride | Problem | None:
    """Let the next breaking production deploy of a listed MCP server ship.

     Records why paying customers' tools may break. The override admits
    one breaking or unverified candidate before it expires (one hour by
    default, at most 24 hours); an app holds at most five open
    overrides. The webhook event mcp.contract.changed and the listing's
    changelog tell customers about the change once it is live.

    Args:
        slug (str):
        body (CreateMCPContractOverrideRequest): Why the next breaking candidate may ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MCPContractOverride | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
