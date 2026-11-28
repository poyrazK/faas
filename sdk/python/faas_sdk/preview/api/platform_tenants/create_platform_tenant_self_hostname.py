from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_platform_tenant_self_hostname_request import CreatePlatformTenantSelfHostnameRequest
from ...models.platform_tenant_self_hostname_response import PlatformTenantSelfHostnameResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreatePlatformTenantSelfHostnameRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/hostnames",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantSelfHostnameResponse | Problem | None:
    if response.status_code == 202:
        response_202 = PlatformTenantSelfHostnameResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantSelfHostnameResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantSelfHostnameRequest,
) -> Response[PlatformTenantSelfHostnameResponse | Problem]:
    """Request a hostname on one of the caller tenant's linked surfaces.

     Requires platform_tenant:hostnames:manage. Hostnames must match the platform owner's delegated DNS
    suffix policy, remain within account/plan quotas, and pass DNS TXT ownership verification. The same
    request safely replays its pending challenge; the response is never cached.

    Args:
        body (CreatePlatformTenantSelfHostnameRequest): Add a DNS hostname to an existing surface
            already linked to the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfHostnameResponse | Problem]
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
    body: CreatePlatformTenantSelfHostnameRequest,
) -> PlatformTenantSelfHostnameResponse | Problem | None:
    """Request a hostname on one of the caller tenant's linked surfaces.

     Requires platform_tenant:hostnames:manage. Hostnames must match the platform owner's delegated DNS
    suffix policy, remain within account/plan quotas, and pass DNS TXT ownership verification. The same
    request safely replays its pending challenge; the response is never cached.

    Args:
        body (CreatePlatformTenantSelfHostnameRequest): Add a DNS hostname to an existing surface
            already linked to the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfHostnameResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantSelfHostnameRequest,
) -> Response[PlatformTenantSelfHostnameResponse | Problem]:
    """Request a hostname on one of the caller tenant's linked surfaces.

     Requires platform_tenant:hostnames:manage. Hostnames must match the platform owner's delegated DNS
    suffix policy, remain within account/plan quotas, and pass DNS TXT ownership verification. The same
    request safely replays its pending challenge; the response is never cached.

    Args:
        body (CreatePlatformTenantSelfHostnameRequest): Add a DNS hostname to an existing surface
            already linked to the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfHostnameResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantSelfHostnameRequest,
) -> PlatformTenantSelfHostnameResponse | Problem | None:
    """Request a hostname on one of the caller tenant's linked surfaces.

     Requires platform_tenant:hostnames:manage. Hostnames must match the platform owner's delegated DNS
    suffix policy, remain within account/plan quotas, and pass DNS TXT ownership verification. The same
    request safely replays its pending challenge; the response is never cached.

    Args:
        body (CreatePlatformTenantSelfHostnameRequest): Add a DNS hostname to an existing surface
            already linked to the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfHostnameResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
