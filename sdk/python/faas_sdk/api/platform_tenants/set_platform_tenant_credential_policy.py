from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_credential_policy_response import PlatformTenantCredentialPolicyResponse
from ...models.problem import Problem
from ...models.set_platform_tenant_credential_policy_request import SetPlatformTenantCredentialPolicyRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: SetPlatformTenantCredentialPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/platform-tenants/{id}/credential-policy".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantCredentialPolicyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantCredentialPolicyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantCredentialPolicyResponse | Problem]:
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
    body: SetPlatformTenantCredentialPolicyRequest,
) -> Response[PlatformTenantCredentialPolicyResponse | Problem]:
    """Replace a customer's delegated credential policy.

     Requires deploy:write and recent MFA. Delegated scopes are limited to read, write, and admin; zero
    keys per consumer is only valid with an empty scope list.

    Args:
        id (UUID):
        body (SetPlatformTenantCredentialPolicyRequest): Explicit scopes and per-consumer key
            ceiling for tenant-bound credential self-service; empty scopes and zero disable it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantCredentialPolicyResponse | Problem]
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
    body: SetPlatformTenantCredentialPolicyRequest,
) -> PlatformTenantCredentialPolicyResponse | Problem | None:
    """Replace a customer's delegated credential policy.

     Requires deploy:write and recent MFA. Delegated scopes are limited to read, write, and admin; zero
    keys per consumer is only valid with an empty scope list.

    Args:
        id (UUID):
        body (SetPlatformTenantCredentialPolicyRequest): Explicit scopes and per-consumer key
            ceiling for tenant-bound credential self-service; empty scopes and zero disable it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantCredentialPolicyResponse | Problem
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
    body: SetPlatformTenantCredentialPolicyRequest,
) -> Response[PlatformTenantCredentialPolicyResponse | Problem]:
    """Replace a customer's delegated credential policy.

     Requires deploy:write and recent MFA. Delegated scopes are limited to read, write, and admin; zero
    keys per consumer is only valid with an empty scope list.

    Args:
        id (UUID):
        body (SetPlatformTenantCredentialPolicyRequest): Explicit scopes and per-consumer key
            ceiling for tenant-bound credential self-service; empty scopes and zero disable it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantCredentialPolicyResponse | Problem]
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
    body: SetPlatformTenantCredentialPolicyRequest,
) -> PlatformTenantCredentialPolicyResponse | Problem | None:
    """Replace a customer's delegated credential policy.

     Requires deploy:write and recent MFA. Delegated scopes are limited to read, write, and admin; zero
    keys per consumer is only valid with an empty scope list.

    Args:
        id (UUID):
        body (SetPlatformTenantCredentialPolicyRequest): Explicit scopes and per-consumer key
            ceiling for tenant-bound credential self-service; empty scopes and zero disable it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantCredentialPolicyResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
