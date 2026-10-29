from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_platform_tenant_reconciliation_request import ApplyPlatformTenantReconciliationRequest
from ...models.platform_tenant_reconciliation_apply_response import PlatformTenantReconciliationApplyResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: ApplyPlatformTenantReconciliationRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/reconciliation-plan/apply".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantReconciliationApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantReconciliationApplyResponse.from_dict(response.json())

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
) -> Response[PlatformTenantReconciliationApplyResponse | Problem]:
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
    body: ApplyPlatformTenantReconciliationRequest,
    idempotency_key: str,
) -> Response[PlatformTenantReconciliationApplyResponse | Problem]:
    """Apply a current, explicitly confirmed tenant reconciliation plan.

     Recomputes the ownership-aware plan inside the write transaction and
    applies it only when `expected_plan_hash` matches the current preview.
    The request requires an `Idempotency-Key`. Managed consumers and
    surfaces are detached rather than deleted; omitted managed hostnames
    declared through `surfaces` are removed. Unmanaged resources are never
    changed. A stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantReconciliationRequest): Desired bundle and plan digest returned
            by the read-only reconciliation-plan endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantReconciliationApplyResponse | Problem]
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
    body: ApplyPlatformTenantReconciliationRequest,
    idempotency_key: str,
) -> PlatformTenantReconciliationApplyResponse | Problem | None:
    """Apply a current, explicitly confirmed tenant reconciliation plan.

     Recomputes the ownership-aware plan inside the write transaction and
    applies it only when `expected_plan_hash` matches the current preview.
    The request requires an `Idempotency-Key`. Managed consumers and
    surfaces are detached rather than deleted; omitted managed hostnames
    declared through `surfaces` are removed. Unmanaged resources are never
    changed. A stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantReconciliationRequest): Desired bundle and plan digest returned
            by the read-only reconciliation-plan endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantReconciliationApplyResponse | Problem
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
    body: ApplyPlatformTenantReconciliationRequest,
    idempotency_key: str,
) -> Response[PlatformTenantReconciliationApplyResponse | Problem]:
    """Apply a current, explicitly confirmed tenant reconciliation plan.

     Recomputes the ownership-aware plan inside the write transaction and
    applies it only when `expected_plan_hash` matches the current preview.
    The request requires an `Idempotency-Key`. Managed consumers and
    surfaces are detached rather than deleted; omitted managed hostnames
    declared through `surfaces` are removed. Unmanaged resources are never
    changed. A stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantReconciliationRequest): Desired bundle and plan digest returned
            by the read-only reconciliation-plan endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantReconciliationApplyResponse | Problem]
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
    body: ApplyPlatformTenantReconciliationRequest,
    idempotency_key: str,
) -> PlatformTenantReconciliationApplyResponse | Problem | None:
    """Apply a current, explicitly confirmed tenant reconciliation plan.

     Recomputes the ownership-aware plan inside the write transaction and
    applies it only when `expected_plan_hash` matches the current preview.
    The request requires an `Idempotency-Key`. Managed consumers and
    surfaces are detached rather than deleted; omitted managed hostnames
    declared through `surfaces` are removed. Unmanaged resources are never
    changed. A stale plan returns 409 and makes no changes.

    Args:
        id (UUID):
        idempotency_key (str):
        body (ApplyPlatformTenantReconciliationRequest): Desired bundle and plan digest returned
            by the read-only reconciliation-plan endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantReconciliationApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
