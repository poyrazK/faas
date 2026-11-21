from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_o_auth_authorization_request import ToolOAuthAuthorizationRequest
from ...models.tool_o_auth_authorization_response import ToolOAuthAuthorizationResponse
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: ToolOAuthAuthorizationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/tool-oauth-connections/{id}/authorization".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ToolOAuthAuthorizationResponse | None:
    if response.status_code == 200:
        response_200 = ToolOAuthAuthorizationResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolOAuthAuthorizationResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ToolOAuthAuthorizationRequest,
) -> Response[Any | ToolOAuthAuthorizationResponse]:
    """Begin or complete owner-approved execution OAuth with S256 PKCE

     Owner and MFA policy required. One authorization attempt per fresh guided connection. Begin returns
    an expiring browser URL bound to an exact IPv4 loopback callback. Same pending begin and callback
    return the original URL. Completion consumes state and records an exclusive lease before code
    exchange; no code exchange is replayed after consumption, failure or restart. Issued tokens are
    sealed directly in Account-domain state, never app environment secrets. Successful provider
    authorization does not grant an agent access to any tool. Reconnect requires a new connection UUID
    and exact tool grants. Provider-side revocation is separate.

    Args:
        id (UUID):
        body (ToolOAuthAuthorizationRequest): Owner-only transient PKCE command; codes and
            callback state are never journaled as tool arguments.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolOAuthAuthorizationResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ToolOAuthAuthorizationRequest,
) -> Any | ToolOAuthAuthorizationResponse | None:
    """Begin or complete owner-approved execution OAuth with S256 PKCE

     Owner and MFA policy required. One authorization attempt per fresh guided connection. Begin returns
    an expiring browser URL bound to an exact IPv4 loopback callback. Same pending begin and callback
    return the original URL. Completion consumes state and records an exclusive lease before code
    exchange; no code exchange is replayed after consumption, failure or restart. Issued tokens are
    sealed directly in Account-domain state, never app environment secrets. Successful provider
    authorization does not grant an agent access to any tool. Reconnect requires a new connection UUID
    and exact tool grants. Provider-side revocation is separate.

    Args:
        id (UUID):
        body (ToolOAuthAuthorizationRequest): Owner-only transient PKCE command; codes and
            callback state are never journaled as tool arguments.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolOAuthAuthorizationResponse
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ToolOAuthAuthorizationRequest,
) -> Response[Any | ToolOAuthAuthorizationResponse]:
    """Begin or complete owner-approved execution OAuth with S256 PKCE

     Owner and MFA policy required. One authorization attempt per fresh guided connection. Begin returns
    an expiring browser URL bound to an exact IPv4 loopback callback. Same pending begin and callback
    return the original URL. Completion consumes state and records an exclusive lease before code
    exchange; no code exchange is replayed after consumption, failure or restart. Issued tokens are
    sealed directly in Account-domain state, never app environment secrets. Successful provider
    authorization does not grant an agent access to any tool. Reconnect requires a new connection UUID
    and exact tool grants. Provider-side revocation is separate.

    Args:
        id (UUID):
        body (ToolOAuthAuthorizationRequest): Owner-only transient PKCE command; codes and
            callback state are never journaled as tool arguments.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolOAuthAuthorizationResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ToolOAuthAuthorizationRequest,
) -> Any | ToolOAuthAuthorizationResponse | None:
    """Begin or complete owner-approved execution OAuth with S256 PKCE

     Owner and MFA policy required. One authorization attempt per fresh guided connection. Begin returns
    an expiring browser URL bound to an exact IPv4 loopback callback. Same pending begin and callback
    return the original URL. Completion consumes state and records an exclusive lease before code
    exchange; no code exchange is replayed after consumption, failure or restart. Issued tokens are
    sealed directly in Account-domain state, never app environment secrets. Successful provider
    authorization does not grant an agent access to any tool. Reconnect requires a new connection UUID
    and exact tool grants. Provider-side revocation is separate.

    Args:
        id (UUID):
        body (ToolOAuthAuthorizationRequest): Owner-only transient PKCE command; codes and
            callback state are never journaled as tool arguments.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolOAuthAuthorizationResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
