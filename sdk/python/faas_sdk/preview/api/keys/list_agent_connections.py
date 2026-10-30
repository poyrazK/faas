from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_connections_response import AgentConnectionsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/agent-connections",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentConnectionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AgentConnectionsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentConnectionsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AgentConnectionsResponse | Problem]:
    """List the agents connected to the account.

     The account's connected agents (ADR-20261030133644), newest first:
    hosted MCP sign-ins, whose `mcp-login` keys the hosted MCP server
    holds, and API keys with the `agent` scope. Only keys that still work
    are listed. A hosted sign-in shows the client name its consent page
    showed and the site the client was verified at, when known.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentConnectionsResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> AgentConnectionsResponse | Problem | None:
    """List the agents connected to the account.

     The account's connected agents (ADR-20261030133644), newest first:
    hosted MCP sign-ins, whose `mcp-login` keys the hosted MCP server
    holds, and API keys with the `agent` scope. Only keys that still work
    are listed. A hosted sign-in shows the client name its consent page
    showed and the site the client was verified at, when known.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentConnectionsResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AgentConnectionsResponse | Problem]:
    """List the agents connected to the account.

     The account's connected agents (ADR-20261030133644), newest first:
    hosted MCP sign-ins, whose `mcp-login` keys the hosted MCP server
    holds, and API keys with the `agent` scope. Only keys that still work
    are listed. A hosted sign-in shows the client name its consent page
    showed and the site the client was verified at, when known.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentConnectionsResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> AgentConnectionsResponse | Problem | None:
    """List the agents connected to the account.

     The account's connected agents (ADR-20261030133644), newest first:
    hosted MCP sign-ins, whose `mcp-login` keys the hosted MCP server
    holds, and API keys with the `agent` scope. Only keys that still work
    are listed. A hosted sign-in shows the client name its consent page
    showed and the site the client was verified at, when known.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentConnectionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
