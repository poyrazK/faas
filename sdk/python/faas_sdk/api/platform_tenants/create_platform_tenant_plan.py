from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_plan_request import PlatformTenantPlanRequest
from ...models.platform_tenant_plan_response import PlatformTenantPlanResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: PlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenant-plans",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantPlanResponse | Problem | None:
    if response.status_code == 201:
        response_201 = PlatformTenantPlanResponse.from_dict(response.json())

        return response_201

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[PlatformTenantPlanResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantPlanResponse | Problem]:
    """Create a reusable tenant plan of rate card terms and a request budget.

     An account keeps at most 100 tenant plans. A plan changes no tenant until it is assigned.

    Args:
        idempotency_key (str | Unset):
        body (PlatformTenantPlanRequest): A reusable tenant plan (ADR-1307): rate card terms and
            an optional request budget that assigning the plan applies.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantPlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: PlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantPlanResponse | Problem | None:
    """Create a reusable tenant plan of rate card terms and a request budget.

     An account keeps at most 100 tenant plans. A plan changes no tenant until it is assigned.

    Args:
        idempotency_key (str | Unset):
        body (PlatformTenantPlanRequest): A reusable tenant plan (ADR-1307): rate card terms and
            an optional request budget that assigning the plan applies.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantPlanResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantPlanResponse | Problem]:
    """Create a reusable tenant plan of rate card terms and a request budget.

     An account keeps at most 100 tenant plans. A plan changes no tenant until it is assigned.

    Args:
        idempotency_key (str | Unset):
        body (PlatformTenantPlanRequest): A reusable tenant plan (ADR-1307): rate card terms and
            an optional request budget that assigning the plan applies.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantPlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PlatformTenantPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantPlanResponse | Problem | None:
    """Create a reusable tenant plan of rate card terms and a request budget.

     An account keeps at most 100 tenant plans. A plan changes no tenant until it is assigned.

    Args:
        idempotency_key (str | Unset):
        body (PlatformTenantPlanRequest): A reusable tenant plan (ADR-1307): rate card terms and
            an optional request budget that assigning the plan applies.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantPlanResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
