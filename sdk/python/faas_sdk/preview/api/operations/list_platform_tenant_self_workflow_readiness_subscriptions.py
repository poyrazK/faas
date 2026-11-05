from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_readiness_subscriptions_response import (
    OperationWorkflowReadinessSubscriptionsResponse,
)
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    *,
    scope: str,
    app_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-readiness/subscriptions",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessSubscriptionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessSubscriptionsResponse.from_dict(response.json())

        return response_200

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
) -> Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
) -> Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]:
    """Customer list WorkflowReadinessSubscriptions

     Customer durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        scope (str):
        app_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        scope=scope,
        app_id=app_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
) -> OperationWorkflowReadinessSubscriptionsResponse | Problem | None:
    """Customer list WorkflowReadinessSubscriptions

     Customer durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        scope (str):
        app_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessSubscriptionsResponse | Problem
    """

    return sync_detailed(
        client=client,
        scope=scope,
        app_id=app_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
) -> Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]:
    """Customer list WorkflowReadinessSubscriptions

     Customer durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        scope (str):
        app_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        scope=scope,
        app_id=app_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
) -> OperationWorkflowReadinessSubscriptionsResponse | Problem | None:
    """Customer list WorkflowReadinessSubscriptions

     Customer durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        scope (str):
        app_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessSubscriptionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            scope=scope,
            app_id=app_id,
        )
    ).parsed
