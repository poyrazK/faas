from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_readiness_subscriptions_response import (
    OperationWorkflowReadinessSubscriptionsResponse,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-readiness/subscriptions".format(
            slug=quote(str(slug), safe=""),
        ),
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
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]:
    """Account list WorkflowReadinessSubscriptions

     Account durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires account read scope and MFA.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> OperationWorkflowReadinessSubscriptionsResponse | Problem | None:
    """Account list WorkflowReadinessSubscriptions

     Account durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires account read scope and MFA.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessSubscriptionsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        tenant_id=tenant_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]:
    """Account list WorkflowReadinessSubscriptions

     Account durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires account read scope and MFA.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessSubscriptionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> OperationWorkflowReadinessSubscriptionsResponse | Problem | None:
    """Account list WorkflowReadinessSubscriptions

     Account durable readiness subscription list operation. Observations are bounded and advisory;
    execution remains application-owned. Requires account read scope and MFA.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessSubscriptionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            tenant_id=tenant_id,
        )
    ).parsed
