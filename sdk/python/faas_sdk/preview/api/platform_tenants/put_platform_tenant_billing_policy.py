from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_billing_policy_response import PlatformTenantBillingPolicyResponse
from ...models.problem import Problem
from ...models.put_api_consumer_billing_policy_request import PutAPIConsumerBillingPolicyRequest
from ...types import Response


def _get_kwargs(
    *,
    body: PutAPIConsumerBillingPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/platform-tenant-billing-policy",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantBillingPolicyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantBillingPolicyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantBillingPolicyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> Response[PlatformTenantBillingPolicyResponse | Problem]:
    """Refuse tenants whose exported invoice is unpaid some days after its due date.

     Every 5 minutes the API places a hold on each tenant whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers every request of a held tenant
    (its consumers, surfaces and JWT rules) with 402
    `tenant_invoice_overdue`.

    Args:
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantBillingPolicyResponse | Problem]
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
    body: PutAPIConsumerBillingPolicyRequest,
) -> PlatformTenantBillingPolicyResponse | Problem | None:
    """Refuse tenants whose exported invoice is unpaid some days after its due date.

     Every 5 minutes the API places a hold on each tenant whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers every request of a held tenant
    (its consumers, surfaces and JWT rules) with 402
    `tenant_invoice_overdue`.

    Args:
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantBillingPolicyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> Response[PlatformTenantBillingPolicyResponse | Problem]:
    """Refuse tenants whose exported invoice is unpaid some days after its due date.

     Every 5 minutes the API places a hold on each tenant whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers every request of a held tenant
    (its consumers, surfaces and JWT rules) with 402
    `tenant_invoice_overdue`.

    Args:
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantBillingPolicyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> PlatformTenantBillingPolicyResponse | Problem | None:
    """Refuse tenants whose exported invoice is unpaid some days after its due date.

     Every 5 minutes the API places a hold on each tenant whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers every request of a held tenant
    (its consumers, surfaces and JWT rules) with 402
    `tenant_invoice_overdue`.

    Args:
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantBillingPolicyResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
