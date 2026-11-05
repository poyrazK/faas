from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_platform_tenant_request import CreatePlatformTenantRequest
from ...models.platform_tenant_response import PlatformTenantResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreatePlatformTenantRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = PlatformTenantResponse.from_dict(response.json())

        return response_201

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantRequest,
) -> Response[PlatformTenantResponse | Problem]:
    """Register an account-level end customer idempotently by external_ref.

    Args:
        body (CreatePlatformTenantRequest): Register a stable external customer reference under
            this account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantResponse | Problem]
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
    body: CreatePlatformTenantRequest,
) -> PlatformTenantResponse | Problem | None:
    """Register an account-level end customer idempotently by external_ref.

    Args:
        body (CreatePlatformTenantRequest): Register a stable external customer reference under
            this account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantRequest,
) -> Response[PlatformTenantResponse | Problem]:
    """Register an account-level end customer idempotently by external_ref.

    Args:
        body (CreatePlatformTenantRequest): Register a stable external customer reference under
            this account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantRequest,
) -> PlatformTenantResponse | Problem | None:
    """Register an account-level end customer idempotently by external_ref.

    Args:
        body (CreatePlatformTenantRequest): Register a stable external customer reference under
            this account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
