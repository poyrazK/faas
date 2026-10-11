from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.check_platform_tenant_self_workflow_readiness_body import CheckPlatformTenantSelfWorkflowReadinessBody
from ...models.operation_workflow_readiness_response import OperationWorkflowReadinessResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CheckPlatformTenantSelfWorkflowReadinessBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/workflow-readiness",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowReadinessResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CheckPlatformTenantSelfWorkflowReadinessBody,
) -> Response[OperationWorkflowReadinessResponse | Problem]:
    """Customer transition readiness — Check reported requirements for a proposed workflow transition.

     Requires platform-tenant operations read scope. app_id is mandatory and tenant_id must be omitted;
    ownership comes from the authenticated customer. Evaluates a proposed declared edge against retained
    state, target-specific blockers, direct prerequisites and planned milestone names. Ready is
    observational and grants no execution authority. Planned names are not committed evidence;
    transaction-time business checks and report validation still apply. A denied readiness result is a
    successful HTTP 200 response.

    Args:
        body (CheckPlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessResponse | Problem]
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
    body: CheckPlatformTenantSelfWorkflowReadinessBody,
) -> OperationWorkflowReadinessResponse | Problem | None:
    """Customer transition readiness — Check reported requirements for a proposed workflow transition.

     Requires platform-tenant operations read scope. app_id is mandatory and tenant_id must be omitted;
    ownership comes from the authenticated customer. Evaluates a proposed declared edge against retained
    state, target-specific blockers, direct prerequisites and planned milestone names. Ready is
    observational and grants no execution authority. Planned names are not committed evidence;
    transaction-time business checks and report validation still apply. A denied readiness result is a
    successful HTTP 200 response.

    Args:
        body (CheckPlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CheckPlatformTenantSelfWorkflowReadinessBody,
) -> Response[OperationWorkflowReadinessResponse | Problem]:
    """Customer transition readiness — Check reported requirements for a proposed workflow transition.

     Requires platform-tenant operations read scope. app_id is mandatory and tenant_id must be omitted;
    ownership comes from the authenticated customer. Evaluates a proposed declared edge against retained
    state, target-specific blockers, direct prerequisites and planned milestone names. Ready is
    observational and grants no execution authority. Planned names are not committed evidence;
    transaction-time business checks and report validation still apply. A denied readiness result is a
    successful HTTP 200 response.

    Args:
        body (CheckPlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CheckPlatformTenantSelfWorkflowReadinessBody,
) -> OperationWorkflowReadinessResponse | Problem | None:
    """Customer transition readiness — Check reported requirements for a proposed workflow transition.

     Requires platform-tenant operations read scope. app_id is mandatory and tenant_id must be omitted;
    ownership comes from the authenticated customer. Evaluates a proposed declared edge against retained
    state, target-specific blockers, direct prerequisites and planned milestone names. Ready is
    observational and grants no execution authority. Planned names are not committed evidence;
    transaction-time business checks and report validation still apply. A denied readiness result is a
    successful HTTP 200 response.

    Args:
        body (CheckPlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
