from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_access_token_response import PlatformTenantAccessTokenResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    token_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/account/platform-tenants/{id}/access-tokens/{token_id}".format(
            id=quote(str(id), safe=""),
            token_id=quote(str(token_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantAccessTokenResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantAccessTokenResponse.from_dict(response.json())

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
) -> Response[PlatformTenantAccessTokenResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    token_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantAccessTokenResponse | Problem]:
    """Revoke a downstream tenant access token.

    Args:
        id (UUID):
        token_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantAccessTokenResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        token_id=token_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    token_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantAccessTokenResponse | Problem | None:
    """Revoke a downstream tenant access token.

    Args:
        id (UUID):
        token_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantAccessTokenResponse | Problem
    """

    return sync_detailed(
        id=id,
        token_id=token_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    token_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantAccessTokenResponse | Problem]:
    """Revoke a downstream tenant access token.

    Args:
        id (UUID):
        token_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantAccessTokenResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        token_id=token_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    token_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantAccessTokenResponse | Problem | None:
    """Revoke a downstream tenant access token.

    Args:
        id (UUID):
        token_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantAccessTokenResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            token_id=token_id,
            client=client,
        )
    ).parsed
