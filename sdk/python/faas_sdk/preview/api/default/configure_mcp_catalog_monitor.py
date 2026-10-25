from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_catalog_monitor_config_request import MCPCatalogMonitorConfigRequest
from ...models.mcp_catalog_monitor_snapshot import MCPCatalogMonitorSnapshot
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: MCPCatalogMonitorConfigRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/mcp-catalog-monitor".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPCatalogMonitorSnapshot | None:
    if response.status_code == 200:
        response_200 = MCPCatalogMonitorSnapshot.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPCatalogMonitorSnapshot]:
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
    body: MCPCatalogMonitorConfigRequest,
) -> Response[Any | MCPCatalogMonitorSnapshot]:
    """Configure a reviewed role baseline and optional managed observer; secret binding also requires
    secrets-read scope

    Args:
        slug (str):
        body (MCPCatalogMonitorConfigRequest): Reviewed baseline and observer settings for one
            independently monitored MCP role.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPCatalogMonitorSnapshot]
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
    body: MCPCatalogMonitorConfigRequest,
) -> Any | MCPCatalogMonitorSnapshot | None:
    """Configure a reviewed role baseline and optional managed observer; secret binding also requires
    secrets-read scope

    Args:
        slug (str):
        body (MCPCatalogMonitorConfigRequest): Reviewed baseline and observer settings for one
            independently monitored MCP role.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPCatalogMonitorSnapshot
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
    body: MCPCatalogMonitorConfigRequest,
) -> Response[Any | MCPCatalogMonitorSnapshot]:
    """Configure a reviewed role baseline and optional managed observer; secret binding also requires
    secrets-read scope

    Args:
        slug (str):
        body (MCPCatalogMonitorConfigRequest): Reviewed baseline and observer settings for one
            independently monitored MCP role.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPCatalogMonitorSnapshot]
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
    body: MCPCatalogMonitorConfigRequest,
) -> Any | MCPCatalogMonitorSnapshot | None:
    """Configure a reviewed role baseline and optional managed observer; secret binding also requires
    secrets-read scope

    Args:
        slug (str):
        body (MCPCatalogMonitorConfigRequest): Reviewed baseline and observer settings for one
            independently monitored MCP role.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPCatalogMonitorSnapshot
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
