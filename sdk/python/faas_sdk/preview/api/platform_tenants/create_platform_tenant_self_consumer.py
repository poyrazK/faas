from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_platform_tenant_self_consumer_request import CreatePlatformTenantSelfConsumerRequest
from ...models.platform_tenant_self_consumer_response import PlatformTenantSelfConsumerResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreatePlatformTenantSelfConsumerRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/consumers",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantSelfConsumerResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantSelfConsumerResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = PlatformTenantSelfConsumerResponse.from_dict(response.json())

        return response_201

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
) -> Response[PlatformTenantSelfConsumerResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantSelfConsumerRequest,
) -> Response[PlatformTenantSelfConsumerResponse | Problem]:
    """Create or replay a customer identity on a linked surface.

     Requires a tenant-bound token with platform_tenant:consumers:manage and an owner-enabled
    provisioning policy. The tenant comes from the bearer; surface_id must already be linked and active.
    App and tenant IDs are never accepted from the request. An identical retry returns the original
    identity without consuming another slot.

    Args:
        body (CreatePlatformTenantSelfConsumerRequest): Create a customer identity on an active
            surface already linked to the token's tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfConsumerResponse | Problem]
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
    body: CreatePlatformTenantSelfConsumerRequest,
) -> PlatformTenantSelfConsumerResponse | Problem | None:
    """Create or replay a customer identity on a linked surface.

     Requires a tenant-bound token with platform_tenant:consumers:manage and an owner-enabled
    provisioning policy. The tenant comes from the bearer; surface_id must already be linked and active.
    App and tenant IDs are never accepted from the request. An identical retry returns the original
    identity without consuming another slot.

    Args:
        body (CreatePlatformTenantSelfConsumerRequest): Create a customer identity on an active
            surface already linked to the token's tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfConsumerResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantSelfConsumerRequest,
) -> Response[PlatformTenantSelfConsumerResponse | Problem]:
    """Create or replay a customer identity on a linked surface.

     Requires a tenant-bound token with platform_tenant:consumers:manage and an owner-enabled
    provisioning policy. The tenant comes from the bearer; surface_id must already be linked and active.
    App and tenant IDs are never accepted from the request. An identical retry returns the original
    identity without consuming another slot.

    Args:
        body (CreatePlatformTenantSelfConsumerRequest): Create a customer identity on an active
            surface already linked to the token's tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfConsumerResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePlatformTenantSelfConsumerRequest,
) -> PlatformTenantSelfConsumerResponse | Problem | None:
    """Create or replay a customer identity on a linked surface.

     Requires a tenant-bound token with platform_tenant:consumers:manage and an owner-enabled
    provisioning policy. The tenant comes from the bearer; surface_id must already be linked and active.
    App and tenant IDs are never accepted from the request. An identical retry returns the original
    identity without consuming another slot.

    Args:
        body (CreatePlatformTenantSelfConsumerRequest): Create a customer identity on an active
            surface already linked to the token's tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfConsumerResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
