from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_consumer_provisioning_policy_response import (
    PlatformTenantConsumerProvisioningPolicyResponse,
)
from ...models.problem import Problem
from ...models.set_platform_tenant_consumer_provisioning_policy_request import (
    SetPlatformTenantConsumerProvisioningPolicyRequest,
)
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: SetPlatformTenantConsumerProvisioningPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/platform-tenants/{id}/consumer-provisioning-policy".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantConsumerProvisioningPolicyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantConsumerProvisioningPolicyResponse.from_dict(response.json())

        return response_200

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
) -> Response[PlatformTenantConsumerProvisioningPolicyResponse | Problem]:
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
    body: SetPlatformTenantConsumerProvisioningPolicyRequest,
) -> Response[PlatformTenantConsumerProvisioningPolicyResponse | Problem]:
    """Replace a customer's self-service customer-provisioning policy.

     Requires deploy:write and recent MFA. Provisioning is disabled with enabled=false and
    max_consumers=0; enabled policies require a 1-100000 customer cap.

    Args:
        id (UUID):
        body (SetPlatformTenantConsumerProvisioningPolicyRequest): Owner-controlled enablement and
            total active-customer ceiling for a downstream tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantConsumerProvisioningPolicyResponse | Problem]
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
    body: SetPlatformTenantConsumerProvisioningPolicyRequest,
) -> PlatformTenantConsumerProvisioningPolicyResponse | Problem | None:
    """Replace a customer's self-service customer-provisioning policy.

     Requires deploy:write and recent MFA. Provisioning is disabled with enabled=false and
    max_consumers=0; enabled policies require a 1-100000 customer cap.

    Args:
        id (UUID):
        body (SetPlatformTenantConsumerProvisioningPolicyRequest): Owner-controlled enablement and
            total active-customer ceiling for a downstream tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantConsumerProvisioningPolicyResponse | Problem
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
    body: SetPlatformTenantConsumerProvisioningPolicyRequest,
) -> Response[PlatformTenantConsumerProvisioningPolicyResponse | Problem]:
    """Replace a customer's self-service customer-provisioning policy.

     Requires deploy:write and recent MFA. Provisioning is disabled with enabled=false and
    max_consumers=0; enabled policies require a 1-100000 customer cap.

    Args:
        id (UUID):
        body (SetPlatformTenantConsumerProvisioningPolicyRequest): Owner-controlled enablement and
            total active-customer ceiling for a downstream tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantConsumerProvisioningPolicyResponse | Problem]
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
    body: SetPlatformTenantConsumerProvisioningPolicyRequest,
) -> PlatformTenantConsumerProvisioningPolicyResponse | Problem | None:
    """Replace a customer's self-service customer-provisioning policy.

     Requires deploy:write and recent MFA. Provisioning is disabled with enabled=false and
    max_consumers=0; enabled policies require a 1-100000 customer cap.

    Args:
        id (UUID):
        body (SetPlatformTenantConsumerProvisioningPolicyRequest): Owner-controlled enablement and
            total active-customer ceiling for a downstream tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantConsumerProvisioningPolicyResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
