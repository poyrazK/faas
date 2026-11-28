from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_fallback_policy import MCPReadinessFallbackPolicy
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/alerts/{id}/mcp-readiness-fallback".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessFallbackPolicy | None:
    if response.status_code == 200:
        response_200 = MCPReadinessFallbackPolicy.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessFallbackPolicy]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | MCPReadinessFallbackPolicy]:
    """Inspect a readiness fallback destination policy

     Read scope and MFA required. Returns version zero and disabled when unconfigured. Signing
    credentials are masked. The policy applies when an incident first claims delivery; existing incident
    receiver URLs stay pinned.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessFallbackPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | MCPReadinessFallbackPolicy | None:
    """Inspect a readiness fallback destination policy

     Read scope and MFA required. Returns version zero and disabled when unconfigured. Signing
    credentials are masked. The policy applies when an incident first claims delivery; existing incident
    receiver URLs stay pinned.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessFallbackPolicy
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | MCPReadinessFallbackPolicy]:
    """Inspect a readiness fallback destination policy

     Read scope and MFA required. Returns version zero and disabled when unconfigured. Signing
    credentials are masked. The policy applies when an incident first claims delivery; existing incident
    receiver URLs stay pinned.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessFallbackPolicy]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | MCPReadinessFallbackPolicy | None:
    """Inspect a readiness fallback destination policy

     Read scope and MFA required. Returns version zero and disabled when unconfigured. Signing
    credentials are masked. The policy applies when an incident first claims delivery; existing incident
    receiver URLs stay pinned.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessFallbackPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
