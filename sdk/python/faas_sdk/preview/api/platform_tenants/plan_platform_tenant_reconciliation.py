from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.plan_platform_tenant_reconciliation_request import PlanPlatformTenantReconciliationRequest
from ...models.platform_tenant_reconciliation_plan_response import PlatformTenantReconciliationPlanResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: PlanPlatformTenantReconciliationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/reconciliation-plan".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantReconciliationPlanResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantReconciliationPlanResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[PlatformTenantReconciliationPlanResponse | Problem]:
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
    body: PlanPlatformTenantReconciliationRequest,
) -> Response[PlatformTenantReconciliationPlanResponse | Problem]:
    """Preview desired platform-tenant resource changes without applying them.

     Validates a desired bundle and returns deterministic create, link, keep, removal-candidate, and
    unmanaged-retention entries. This endpoint is read-only; omitted managed resources are only
    candidates and are never detached, revoked, or deleted.

    Args:
        id (UUID):
        body (PlanPlatformTenantReconciliationRequest): Complete desired consumer and surface
            bundle for one existing platform tenant. Omitted managed resources appear as removal
            candidates; omitted unmanaged resources are explicitly retained.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantReconciliationPlanResponse | Problem]
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
    body: PlanPlatformTenantReconciliationRequest,
) -> PlatformTenantReconciliationPlanResponse | Problem | None:
    """Preview desired platform-tenant resource changes without applying them.

     Validates a desired bundle and returns deterministic create, link, keep, removal-candidate, and
    unmanaged-retention entries. This endpoint is read-only; omitted managed resources are only
    candidates and are never detached, revoked, or deleted.

    Args:
        id (UUID):
        body (PlanPlatformTenantReconciliationRequest): Complete desired consumer and surface
            bundle for one existing platform tenant. Omitted managed resources appear as removal
            candidates; omitted unmanaged resources are explicitly retained.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantReconciliationPlanResponse | Problem
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
    body: PlanPlatformTenantReconciliationRequest,
) -> Response[PlatformTenantReconciliationPlanResponse | Problem]:
    """Preview desired platform-tenant resource changes without applying them.

     Validates a desired bundle and returns deterministic create, link, keep, removal-candidate, and
    unmanaged-retention entries. This endpoint is read-only; omitted managed resources are only
    candidates and are never detached, revoked, or deleted.

    Args:
        id (UUID):
        body (PlanPlatformTenantReconciliationRequest): Complete desired consumer and surface
            bundle for one existing platform tenant. Omitted managed resources appear as removal
            candidates; omitted unmanaged resources are explicitly retained.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantReconciliationPlanResponse | Problem]
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
    body: PlanPlatformTenantReconciliationRequest,
) -> PlatformTenantReconciliationPlanResponse | Problem | None:
    """Preview desired platform-tenant resource changes without applying them.

     Validates a desired bundle and returns deterministic create, link, keep, removal-candidate, and
    unmanaged-retention entries. This endpoint is read-only; omitted managed resources are only
    candidates and are never detached, revoked, or deleted.

    Args:
        id (UUID):
        body (PlanPlatformTenantReconciliationRequest): Complete desired consumer and surface
            bundle for one existing platform tenant. Omitted managed resources appear as removal
            candidates; omitted unmanaged resources are explicitly retained.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantReconciliationPlanResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
