from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_platform_tenant_request import ApplyPlatformTenantRequest
from ...models.apply_platform_tenant_response import ApplyPlatformTenantResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ApplyPlatformTenantRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/apply",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyPlatformTenantResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyPlatformTenantResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[ApplyPlatformTenantResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantRequest,
) -> Response[ApplyPlatformTenantResponse | Problem]:
    """Atomically reconcile an additive customer onboarding bundle.

     Atomically creates or reuses a platform tenant, app consumers, tenant surfaces, and hostname intent,
    or previews the same checks with dry_run. Existing surface IDs may also be linked. Omitted resources
    are not detached; keys are separate and DNS verification and certificate issuance remain
    asynchronous.

    Args:
        body (ApplyPlatformTenantRequest): Additive, retry-safe onboarding of consumers and
            declarative hostname surfaces; dry_run validates and previews without writes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyPlatformTenantResponse | Problem]
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
    body: ApplyPlatformTenantRequest,
) -> ApplyPlatformTenantResponse | Problem | None:
    """Atomically reconcile an additive customer onboarding bundle.

     Atomically creates or reuses a platform tenant, app consumers, tenant surfaces, and hostname intent,
    or previews the same checks with dry_run. Existing surface IDs may also be linked. Omitted resources
    are not detached; keys are separate and DNS verification and certificate issuance remain
    asynchronous.

    Args:
        body (ApplyPlatformTenantRequest): Additive, retry-safe onboarding of consumers and
            declarative hostname surfaces; dry_run validates and previews without writes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyPlatformTenantResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantRequest,
) -> Response[ApplyPlatformTenantResponse | Problem]:
    """Atomically reconcile an additive customer onboarding bundle.

     Atomically creates or reuses a platform tenant, app consumers, tenant surfaces, and hostname intent,
    or previews the same checks with dry_run. Existing surface IDs may also be linked. Omitted resources
    are not detached; keys are separate and DNS verification and certificate issuance remain
    asynchronous.

    Args:
        body (ApplyPlatformTenantRequest): Additive, retry-safe onboarding of consumers and
            declarative hostname surfaces; dry_run validates and previews without writes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyPlatformTenantResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ApplyPlatformTenantRequest,
) -> ApplyPlatformTenantResponse | Problem | None:
    """Atomically reconcile an additive customer onboarding bundle.

     Atomically creates or reuses a platform tenant, app consumers, tenant surfaces, and hostname intent,
    or previews the same checks with dry_run. Existing surface IDs may also be linked. Omitted resources
    are not detached; keys are separate and DNS verification and certificate issuance remain
    asynchronous.

    Args:
        body (ApplyPlatformTenantRequest): Additive, retry-safe onboarding of consumers and
            declarative hostname surfaces; dry_run validates and previews without writes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyPlatformTenantResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
