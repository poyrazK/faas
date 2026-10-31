from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_client_request import AgentClientRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: AgentClientRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/agent-connections/self",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AgentClientRequest,
) -> Response[Any | Problem]:
    """Name the client of the calling hosted MCP key.

     Called by the hosted MCP server after a browser sign-in, with the
    connection's own `mcp-login` key. Any other credential is refused.
    The first name wins.

    Args:
        body (AgentClientRequest): The client a hosted MCP connection's key belongs to, named once
            by the hosted MCP server.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: AgentClientRequest,
) -> Any | Problem | None:
    """Name the client of the calling hosted MCP key.

     Called by the hosted MCP server after a browser sign-in, with the
    connection's own `mcp-login` key. Any other credential is refused.
    The first name wins.

    Args:
        body (AgentClientRequest): The client a hosted MCP connection's key belongs to, named once
            by the hosted MCP server.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AgentClientRequest,
) -> Response[Any | Problem]:
    """Name the client of the calling hosted MCP key.

     Called by the hosted MCP server after a browser sign-in, with the
    connection's own `mcp-login` key. Any other credential is refused.
    The first name wins.

    Args:
        body (AgentClientRequest): The client a hosted MCP connection's key belongs to, named once
            by the hosted MCP server.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AgentClientRequest,
) -> Any | Problem | None:
    """Name the client of the calling hosted MCP key.

     Called by the hosted MCP server after a browser sign-in, with the
    connection's own `mcp-login` key. Any other credential is refused.
    The first name wins.

    Args:
        body (AgentClientRequest): The client a hosted MCP connection's key belongs to, named once
            by the hosted MCP server.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
