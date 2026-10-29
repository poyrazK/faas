from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.assign_platform_tenant_plan_request import AssignPlatformTenantPlanRequest
from ...models.platform_tenant_plan_assignment_response import PlatformTenantPlanAssignmentResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    body: AssignPlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/plan-assignments".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantPlanAssignmentResponse | Problem | None:
    if response.status_code == 201:
        response_201 = PlatformTenantPlanAssignmentResponse.from_dict(response.json())

        return response_201

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
) -> Response[PlatformTenantPlanAssignmentResponse | Problem]:
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
    body: AssignPlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantPlanAssignmentResponse | Problem]:
    """Move a tenant onto a plan from the next minute.

     Creates the tenant's rate card version from the plan's terms,
    effective the next UTC minute, and sets the plan's request budget at
    once when the plan has one. The tenant's existing currency must match
    the plan's.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (AssignPlatformTenantPlanRequest): Moves a tenant onto a plan from the next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantPlanAssignmentResponse | Problem]
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
    body: AssignPlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantPlanAssignmentResponse | Problem | None:
    """Move a tenant onto a plan from the next minute.

     Creates the tenant's rate card version from the plan's terms,
    effective the next UTC minute, and sets the plan's request budget at
    once when the plan has one. The tenant's existing currency must match
    the plan's.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (AssignPlatformTenantPlanRequest): Moves a tenant onto a plan from the next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantPlanAssignmentResponse | Problem
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
    body: AssignPlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantPlanAssignmentResponse | Problem]:
    """Move a tenant onto a plan from the next minute.

     Creates the tenant's rate card version from the plan's terms,
    effective the next UTC minute, and sets the plan's request budget at
    once when the plan has one. The tenant's existing currency must match
    the plan's.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (AssignPlatformTenantPlanRequest): Moves a tenant onto a plan from the next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantPlanAssignmentResponse | Problem]
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
    body: AssignPlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantPlanAssignmentResponse | Problem | None:
    """Move a tenant onto a plan from the next minute.

     Creates the tenant's rate card version from the plan's terms,
    effective the next UTC minute, and sets the plan's request budget at
    once when the plan has one. The tenant's existing currency must match
    the plan's.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (AssignPlatformTenantPlanRequest): Moves a tenant onto a plan from the next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantPlanAssignmentResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
