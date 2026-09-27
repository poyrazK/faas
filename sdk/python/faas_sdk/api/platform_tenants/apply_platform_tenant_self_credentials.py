from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_platform_tenant_credentials_request import ApplyPlatformTenantCredentialsRequest
from ...models.apply_platform_tenant_credentials_response import ApplyPlatformTenantCredentialsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ApplyPlatformTenantCredentialsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/credentials/apply",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyPlatformTenantCredentialsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyPlatformTenantCredentialsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[ApplyPlatformTenantCredentialsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantCredentialsRequest,
) -> Response[ApplyPlatformTenantCredentialsResponse | Problem]:
    """Reconcile this tenant's downstream consumer keys.

     Requires platform_tenant:credentials:manage. New keys must use an owner-allowed scope and fit the
    owner's active-key cap; revocation remains available even after delegation is disabled. Submit only
    client-generated SHA-256 hashes, never plaintext. Changes are atomic, account/tenant identity comes
    from the bearer, and responses contain metadata only.

    Args:
        body (ApplyPlatformTenantCredentialsRequest): Additive key reconciliation and revocation
            in one transaction; provide at least one key or revocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyPlatformTenantCredentialsResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantCredentialsRequest,
) -> ApplyPlatformTenantCredentialsResponse | Problem | None:
    """Reconcile this tenant's downstream consumer keys.

     Requires platform_tenant:credentials:manage. New keys must use an owner-allowed scope and fit the
    owner's active-key cap; revocation remains available even after delegation is disabled. Submit only
    client-generated SHA-256 hashes, never plaintext. Changes are atomic, account/tenant identity comes
    from the bearer, and responses contain metadata only.

    Args:
        body (ApplyPlatformTenantCredentialsRequest): Additive key reconciliation and revocation
            in one transaction; provide at least one key or revocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyPlatformTenantCredentialsResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantCredentialsRequest,
) -> Response[ApplyPlatformTenantCredentialsResponse | Problem]:
    """Reconcile this tenant's downstream consumer keys.

     Requires platform_tenant:credentials:manage. New keys must use an owner-allowed scope and fit the
    owner's active-key cap; revocation remains available even after delegation is disabled. Submit only
    client-generated SHA-256 hashes, never plaintext. Changes are atomic, account/tenant identity comes
    from the bearer, and responses contain metadata only.

    Args:
        body (ApplyPlatformTenantCredentialsRequest): Additive key reconciliation and revocation
            in one transaction; provide at least one key or revocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyPlatformTenantCredentialsResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantCredentialsRequest,
) -> ApplyPlatformTenantCredentialsResponse | Problem | None:
    """Reconcile this tenant's downstream consumer keys.

     Requires platform_tenant:credentials:manage. New keys must use an owner-allowed scope and fit the
    owner's active-key cap; revocation remains available even after delegation is disabled. Submit only
    client-generated SHA-256 hashes, never plaintext. Changes are atomic, account/tenant identity comes
    from the bearer, and responses contain metadata only.

    Args:
        body (ApplyPlatformTenantCredentialsRequest): Additive key reconciliation and revocation
            in one transaction; provide at least one key or revocation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyPlatformTenantCredentialsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
