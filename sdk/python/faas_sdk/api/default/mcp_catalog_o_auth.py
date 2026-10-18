from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_catalog_o_auth_request import MCPCatalogOAuthRequest
from ...models.mcp_catalog_o_auth_response import MCPCatalogOAuthResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: MCPCatalogOAuthRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mcp-catalog-monitor/oauth".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPCatalogOAuthResponse | None:
    if response.status_code == 200:
        response_200 = MCPCatalogOAuthResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPCatalogOAuthResponse]:
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
    body: MCPCatalogOAuthRequest,
) -> Response[Any | MCPCatalogOAuthResponse]:
    """Begin, complete or disconnect guided OAuth for an observer role; requires deploy intent and secret-
    read permission

    Args:
        slug (str):
        body (MCPCatalogOAuthRequest): Account-authorized guided OAuth action bound to an exact
            role revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPCatalogOAuthResponse]
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
    body: MCPCatalogOAuthRequest,
) -> Any | MCPCatalogOAuthResponse | None:
    """Begin, complete or disconnect guided OAuth for an observer role; requires deploy intent and secret-
    read permission

    Args:
        slug (str):
        body (MCPCatalogOAuthRequest): Account-authorized guided OAuth action bound to an exact
            role revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPCatalogOAuthResponse
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
    body: MCPCatalogOAuthRequest,
) -> Response[Any | MCPCatalogOAuthResponse]:
    """Begin, complete or disconnect guided OAuth for an observer role; requires deploy intent and secret-
    read permission

    Args:
        slug (str):
        body (MCPCatalogOAuthRequest): Account-authorized guided OAuth action bound to an exact
            role revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPCatalogOAuthResponse]
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
    body: MCPCatalogOAuthRequest,
) -> Any | MCPCatalogOAuthResponse | None:
    """Begin, complete or disconnect guided OAuth for an observer role; requires deploy intent and secret-
    read permission

    Args:
        slug (str):
        body (MCPCatalogOAuthRequest): Account-authorized guided OAuth action bound to an exact
            role revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPCatalogOAuthResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
