from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_self_consumer_revocation_response import PlatformTenantSelfConsumerRevocationResponse
from ...models.problem import Problem
from ...models.revoke_platform_tenant_self_consumers_request import RevokePlatformTenantSelfConsumersRequest
from ...types import Response


def _get_kwargs(
    *,
    body: RevokePlatformTenantSelfConsumersRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/consumers/revoke",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantSelfConsumerRevocationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantSelfConsumerRevocationResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[PlatformTenantSelfConsumerRevocationResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: RevokePlatformTenantSelfConsumersRequest,
) -> Response[PlatformTenantSelfConsumerRevocationResponse | Problem]:
    """Revoke selected customers and all of their credentials atomically.

     Requires a tenant-bound token with platform_tenant:consumers:manage. The tenant is derived from the
    bearer; consumer_ids must be IDs returned by this tenant's customer listing. Every ID is validated
    before any change. Revocation remains available when customer provisioning is disabled or the tenant
    is suspended. An exact retry is safe and reports zero newly revoked keys.

    Args:
        body (RevokePlatformTenantSelfConsumersRequest): All-or-nothing batch of customer IDs from
            this tenant's linked-consumer inventory.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfConsumerRevocationResponse | Problem]
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
    body: RevokePlatformTenantSelfConsumersRequest,
) -> PlatformTenantSelfConsumerRevocationResponse | Problem | None:
    """Revoke selected customers and all of their credentials atomically.

     Requires a tenant-bound token with platform_tenant:consumers:manage. The tenant is derived from the
    bearer; consumer_ids must be IDs returned by this tenant's customer listing. Every ID is validated
    before any change. Revocation remains available when customer provisioning is disabled or the tenant
    is suspended. An exact retry is safe and reports zero newly revoked keys.

    Args:
        body (RevokePlatformTenantSelfConsumersRequest): All-or-nothing batch of customer IDs from
            this tenant's linked-consumer inventory.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfConsumerRevocationResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: RevokePlatformTenantSelfConsumersRequest,
) -> Response[PlatformTenantSelfConsumerRevocationResponse | Problem]:
    """Revoke selected customers and all of their credentials atomically.

     Requires a tenant-bound token with platform_tenant:consumers:manage. The tenant is derived from the
    bearer; consumer_ids must be IDs returned by this tenant's customer listing. Every ID is validated
    before any change. Revocation remains available when customer provisioning is disabled or the tenant
    is suspended. An exact retry is safe and reports zero newly revoked keys.

    Args:
        body (RevokePlatformTenantSelfConsumersRequest): All-or-nothing batch of customer IDs from
            this tenant's linked-consumer inventory.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfConsumerRevocationResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: RevokePlatformTenantSelfConsumersRequest,
) -> PlatformTenantSelfConsumerRevocationResponse | Problem | None:
    """Revoke selected customers and all of their credentials atomically.

     Requires a tenant-bound token with platform_tenant:consumers:manage. The tenant is derived from the
    bearer; consumer_ids must be IDs returned by this tenant's customer listing. Every ID is validated
    before any change. Revocation remains available when customer provisioning is disabled or the tenant
    is suspended. An exact retry is safe and reports zero newly revoked keys.

    Args:
        body (RevokePlatformTenantSelfConsumersRequest): All-or-nothing batch of customer IDs from
            this tenant's linked-consumer inventory.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfConsumerRevocationResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
