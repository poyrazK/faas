from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_platform_tenant_access_token_request import CreatePlatformTenantAccessTokenRequest
from ...models.create_platform_tenant_access_token_response import CreatePlatformTenantAccessTokenResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: CreatePlatformTenantAccessTokenRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/access-tokens".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CreatePlatformTenantAccessTokenResponse | Problem | None:
    if response.status_code == 201:
        response_201 = CreatePlatformTenantAccessTokenResponse.from_dict(response.json())

        return response_201

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CreatePlatformTenantAccessTokenResponse | Problem]:
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
    body: CreatePlatformTenantAccessTokenRequest,
) -> Response[CreatePlatformTenantAccessTokenResponse | Problem]:
    """Mint a tenant-bound read-only self-service credential.

     The bearer is scoped to exactly one downstream tenant, supports usage and/or finalized-statement
    reads, expires within 365 days, and is returned once. Account-wide API-key creation cannot mint
    these special tenant scopes. This endpoint does not cache plaintext for Idempotency-Key retries;
    after a lost response, list token metadata and create a replacement under a new name.

    Args:
        id (UUID):
        body (CreatePlatformTenantAccessTokenRequest): Mint a bearer for one downstream tenant.
            Plaintext is returned once and expires no later than one year after creation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreatePlatformTenantAccessTokenResponse | Problem]
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
    body: CreatePlatformTenantAccessTokenRequest,
) -> CreatePlatformTenantAccessTokenResponse | Problem | None:
    """Mint a tenant-bound read-only self-service credential.

     The bearer is scoped to exactly one downstream tenant, supports usage and/or finalized-statement
    reads, expires within 365 days, and is returned once. Account-wide API-key creation cannot mint
    these special tenant scopes. This endpoint does not cache plaintext for Idempotency-Key retries;
    after a lost response, list token metadata and create a replacement under a new name.

    Args:
        id (UUID):
        body (CreatePlatformTenantAccessTokenRequest): Mint a bearer for one downstream tenant.
            Plaintext is returned once and expires no later than one year after creation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreatePlatformTenantAccessTokenResponse | Problem
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
    body: CreatePlatformTenantAccessTokenRequest,
) -> Response[CreatePlatformTenantAccessTokenResponse | Problem]:
    """Mint a tenant-bound read-only self-service credential.

     The bearer is scoped to exactly one downstream tenant, supports usage and/or finalized-statement
    reads, expires within 365 days, and is returned once. Account-wide API-key creation cannot mint
    these special tenant scopes. This endpoint does not cache plaintext for Idempotency-Key retries;
    after a lost response, list token metadata and create a replacement under a new name.

    Args:
        id (UUID):
        body (CreatePlatformTenantAccessTokenRequest): Mint a bearer for one downstream tenant.
            Plaintext is returned once and expires no later than one year after creation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreatePlatformTenantAccessTokenResponse | Problem]
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
    body: CreatePlatformTenantAccessTokenRequest,
) -> CreatePlatformTenantAccessTokenResponse | Problem | None:
    """Mint a tenant-bound read-only self-service credential.

     The bearer is scoped to exactly one downstream tenant, supports usage and/or finalized-statement
    reads, expires within 365 days, and is returned once. Account-wide API-key creation cannot mint
    these special tenant scopes. This endpoint does not cache plaintext for Idempotency-Key retries;
    after a lost response, list token metadata and create a replacement under a new name.

    Args:
        id (UUID):
        body (CreatePlatformTenantAccessTokenRequest): Mint a bearer for one downstream tenant.
            Plaintext is returned once and expires no later than one year after creation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreatePlatformTenantAccessTokenResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
