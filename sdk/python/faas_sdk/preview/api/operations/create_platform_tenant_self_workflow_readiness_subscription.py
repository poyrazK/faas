from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_platform_tenant_self_workflow_readiness_subscription_body import (
    CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody,
)
from ...models.operation_workflow_readiness_subscription import OperationWorkflowReadinessSubscription
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/workflow-readiness/subscriptions",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessSubscription | Problem | None:
    if response.status_code == 201:
        response_201 = OperationWorkflowReadinessSubscription.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowReadinessSubscription | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody,
) -> Response[OperationWorkflowReadinessSubscription | Problem]:
    """Customer create WorkflowReadinessSubscription

     Customer durable readiness subscription create operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations manage scope.

    Args:
        body (CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessSubscription | Problem]
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
    client: AuthenticatedClient,
    body: CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody,
) -> OperationWorkflowReadinessSubscription | Problem | None:
    """Customer create WorkflowReadinessSubscription

     Customer durable readiness subscription create operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations manage scope.

    Args:
        body (CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessSubscription | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody,
) -> Response[OperationWorkflowReadinessSubscription | Problem]:
    """Customer create WorkflowReadinessSubscription

     Customer durable readiness subscription create operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations manage scope.

    Args:
        body (CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessSubscription | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody,
) -> OperationWorkflowReadinessSubscription | Problem | None:
    """Customer create WorkflowReadinessSubscription

     Customer durable readiness subscription create operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations manage scope.

    Args:
        body (CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessSubscription | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
