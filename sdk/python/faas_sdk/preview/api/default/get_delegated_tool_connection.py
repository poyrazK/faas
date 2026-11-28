from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_o_auth_connection_status import ToolOAuthConnectionStatus
from ...types import Response


def _get_kwargs(
    grant: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/delegated-tool-connections/{grant}".format(
            grant=quote(str(grant), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ToolOAuthConnectionStatus | None:
    if response.status_code == 200:
        response_200 = ToolOAuthConnectionStatus.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolOAuthConnectionStatus]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    grant: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ToolOAuthConnectionStatus]:
    """Read credential-free execution connection status through an exact active tool grant

     Personal agent key, immutable app/project binding and exact invocation grant are checked on every
    read. No provider configuration, secret names, tokens or ciphertext are returned. Does not trigger
    renewal.

    Args:
        grant (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolOAuthConnectionStatus]
    """

    kwargs = _get_kwargs(
        grant=grant,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    grant: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ToolOAuthConnectionStatus | None:
    """Read credential-free execution connection status through an exact active tool grant

     Personal agent key, immutable app/project binding and exact invocation grant are checked on every
    read. No provider configuration, secret names, tokens or ciphertext are returned. Does not trigger
    renewal.

    Args:
        grant (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolOAuthConnectionStatus
    """

    return sync_detailed(
        grant=grant,
        client=client,
    ).parsed


async def asyncio_detailed(
    grant: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ToolOAuthConnectionStatus]:
    """Read credential-free execution connection status through an exact active tool grant

     Personal agent key, immutable app/project binding and exact invocation grant are checked on every
    read. No provider configuration, secret names, tokens or ciphertext are returned. Does not trigger
    renewal.

    Args:
        grant (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolOAuthConnectionStatus]
    """

    kwargs = _get_kwargs(
        grant=grant,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    grant: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ToolOAuthConnectionStatus | None:
    """Read credential-free execution connection status through an exact active tool grant

     Personal agent key, immutable app/project binding and exact invocation grant are checked on every
    read. No provider configuration, secret names, tokens or ciphertext are returned. Does not trigger
    renewal.

    Args:
        grant (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolOAuthConnectionStatus
    """

    return (
        await asyncio_detailed(
            grant=grant,
            client=client,
        )
    ).parsed
