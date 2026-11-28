from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_mcp_listing_server_json_response_200 import GetMCPListingServerJSONResponse200
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/consumer-signup/{slug}/server.json".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GetMCPListingServerJSONResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = GetMCPListingServerJSONResponse200.from_dict(response.json())

        return response_200

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
) -> Response[GetMCPListingServerJSONResponse200 | Problem]:
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
) -> Response[GetMCPListingServerJSONResponse200 | Problem]:
    """Read a listed MCP server's registry server.json.

     Needs no credential. The MCP registry server.json (schema
    2025-12-11) of a listing with a registry name, with one
    streamable-http remote at the listed endpoint; submit it with
    mcp-publisher.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GetMCPListingServerJSONResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> GetMCPListingServerJSONResponse200 | Problem | None:
    """Read a listed MCP server's registry server.json.

     Needs no credential. The MCP registry server.json (schema
    2025-12-11) of a listing with a registry name, with one
    streamable-http remote at the listed endpoint; submit it with
    mcp-publisher.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GetMCPListingServerJSONResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GetMCPListingServerJSONResponse200 | Problem]:
    """Read a listed MCP server's registry server.json.

     Needs no credential. The MCP registry server.json (schema
    2025-12-11) of a listing with a registry name, with one
    streamable-http remote at the listed endpoint; submit it with
    mcp-publisher.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GetMCPListingServerJSONResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> GetMCPListingServerJSONResponse200 | Problem | None:
    """Read a listed MCP server's registry server.json.

     Needs no credential. The MCP registry server.json (schema
    2025-12-11) of a listing with a registry name, with one
    streamable-http remote at the listed endpoint; submit it with
    mcp-publisher.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GetMCPListingServerJSONResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
