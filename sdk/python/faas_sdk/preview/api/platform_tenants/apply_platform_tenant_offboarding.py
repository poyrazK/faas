from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_platform_tenant_offboarding_request import ApplyPlatformTenantOffboardingRequest
from ...models.platform_tenant_offboarding_apply_response import PlatformTenantOffboardingApplyResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: ApplyPlatformTenantOffboardingRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/offboarding-plan/apply".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantOffboardingApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantOffboardingApplyResponse.from_dict(response.json())

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
) -> Response[PlatformTenantOffboardingApplyResponse | Problem]:
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
    body: ApplyPlatformTenantOffboardingRequest,
    idempotency_key: str,
) -> Response[PlatformTenantOffboardingApplyResponse | Problem]:
    """Apply a current, explicitly confirmed platform-tenant offboarding plan.

     Recomputes the read-only plan while locking the tenant and affected
    resources, then applies all actions and persists a secret-free receipt
    in one transaction only when `expected_plan_hash` matches. The request
    requires an `Idempotency-Key`. Unmanaged resources and usage, billing,
    reconciliation history, and webhook subscriptions are preserved. A
    stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantOffboardingRequest): Confirmation digest returned by the preview
            for the exact offboarding plan being applied.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantOffboardingApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantOffboardingRequest,
    idempotency_key: str,
) -> PlatformTenantOffboardingApplyResponse | Problem | None:
    """Apply a current, explicitly confirmed platform-tenant offboarding plan.

     Recomputes the read-only plan while locking the tenant and affected
    resources, then applies all actions and persists a secret-free receipt
    in one transaction only when `expected_plan_hash` matches. The request
    requires an `Idempotency-Key`. Unmanaged resources and usage, billing,
    reconciliation history, and webhook subscriptions are preserved. A
    stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantOffboardingRequest): Confirmation digest returned by the preview
            for the exact offboarding plan being applied.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantOffboardingApplyResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantOffboardingRequest,
    idempotency_key: str,
) -> Response[PlatformTenantOffboardingApplyResponse | Problem]:
    """Apply a current, explicitly confirmed platform-tenant offboarding plan.

     Recomputes the read-only plan while locking the tenant and affected
    resources, then applies all actions and persists a secret-free receipt
    in one transaction only when `expected_plan_hash` matches. The request
    requires an `Idempotency-Key`. Unmanaged resources and usage, billing,
    reconciliation history, and webhook subscriptions are preserved. A
    stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantOffboardingRequest): Confirmation digest returned by the preview
            for the exact offboarding plan being applied.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantOffboardingApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantOffboardingRequest,
    idempotency_key: str,
) -> PlatformTenantOffboardingApplyResponse | Problem | None:
    """Apply a current, explicitly confirmed platform-tenant offboarding plan.

     Recomputes the read-only plan while locking the tenant and affected
    resources, then applies all actions and persists a secret-free receipt
    in one transaction only when `expected_plan_hash` matches. The request
    requires an `Idempotency-Key`. Unmanaged resources and usage, billing,
    reconciliation history, and webhook subscriptions are preserved. A
    stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantOffboardingRequest): Confirmation digest returned by the preview
            for the exact offboarding plan being applied.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantOffboardingApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
