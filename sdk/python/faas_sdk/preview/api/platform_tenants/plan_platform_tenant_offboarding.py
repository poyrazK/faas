from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_offboarding_plan_response import PlatformTenantOffboardingPlanResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/offboarding-plan".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantOffboardingPlanResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantOffboardingPlanResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[PlatformTenantOffboardingPlanResponse | Problem]:
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
) -> Response[PlatformTenantOffboardingPlanResponse | Problem]:
    """Preview a safe platform-tenant offboarding operation.

     Returns the tenant status, a stable plan hash, and counts of the
    proposed access and ownership changes. The plan describes suspending
    the tenant, revoking linked consumer keys and tenant-bound access
    tokens, disabling delegated provisioning policies, detaching only
    platform-managed consumers and surfaces, and removing only
    platform-managed hostnames. Unmanaged resources are retained. Usage,
    billing statements, reconciliation receipts, and webhook subscriptions
    are preserved. This endpoint is read-only; it does not reserve or
    apply the plan.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantOffboardingPlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantOffboardingPlanResponse | Problem | None:
    """Preview a safe platform-tenant offboarding operation.

     Returns the tenant status, a stable plan hash, and counts of the
    proposed access and ownership changes. The plan describes suspending
    the tenant, revoking linked consumer keys and tenant-bound access
    tokens, disabling delegated provisioning policies, detaching only
    platform-managed consumers and surfaces, and removing only
    platform-managed hostnames. Unmanaged resources are retained. Usage,
    billing statements, reconciliation receipts, and webhook subscriptions
    are preserved. This endpoint is read-only; it does not reserve or
    apply the plan.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantOffboardingPlanResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantOffboardingPlanResponse | Problem]:
    """Preview a safe platform-tenant offboarding operation.

     Returns the tenant status, a stable plan hash, and counts of the
    proposed access and ownership changes. The plan describes suspending
    the tenant, revoking linked consumer keys and tenant-bound access
    tokens, disabling delegated provisioning policies, detaching only
    platform-managed consumers and surfaces, and removing only
    platform-managed hostnames. Unmanaged resources are retained. Usage,
    billing statements, reconciliation receipts, and webhook subscriptions
    are preserved. This endpoint is read-only; it does not reserve or
    apply the plan.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantOffboardingPlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantOffboardingPlanResponse | Problem | None:
    """Preview a safe platform-tenant offboarding operation.

     Returns the tenant status, a stable plan hash, and counts of the
    proposed access and ownership changes. The plan describes suspending
    the tenant, revoking linked consumer keys and tenant-bound access
    tokens, disabling delegated provisioning policies, detaching only
    platform-managed consumers and surfaces, and removing only
    platform-managed hostnames. Unmanaged resources are retained. Usage,
    billing statements, reconciliation receipts, and webhook subscriptions
    are preserved. This endpoint is read-only; it does not reserve or
    apply the plan.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantOffboardingPlanResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
