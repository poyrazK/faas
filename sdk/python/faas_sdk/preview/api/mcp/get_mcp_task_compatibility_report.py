from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_task_compatibility_report_response import MCPTaskCompatibilityReportResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/mcp-task-compatibility".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MCPTaskCompatibilityReportResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MCPTaskCompatibilityReportResponse.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MCPTaskCompatibilityReportResponse | Problem]:
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
) -> Response[MCPTaskCompatibilityReportResponse | Problem]:
    """Read the latest MCP task handler compatibility snapshot

     Returns the latest bounded observer snapshot, including server-computed freshness. An unpublished
    snapshot is returned as published=false and stale=true.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MCPTaskCompatibilityReportResponse | Problem]
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
) -> MCPTaskCompatibilityReportResponse | Problem | None:
    """Read the latest MCP task handler compatibility snapshot

     Returns the latest bounded observer snapshot, including server-computed freshness. An unpublished
    snapshot is returned as published=false and stale=true.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MCPTaskCompatibilityReportResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[MCPTaskCompatibilityReportResponse | Problem]:
    """Read the latest MCP task handler compatibility snapshot

     Returns the latest bounded observer snapshot, including server-computed freshness. An unpublished
    snapshot is returned as published=false and stale=true.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MCPTaskCompatibilityReportResponse | Problem]
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
) -> MCPTaskCompatibilityReportResponse | Problem | None:
    """Read the latest MCP task handler compatibility snapshot

     Returns the latest bounded observer snapshot, including server-computed freshness. An unpublished
    snapshot is returned as published=false and stale=true.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MCPTaskCompatibilityReportResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
