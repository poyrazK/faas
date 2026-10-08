from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_action_preview_response import OperationWorkflowActionPreviewResponse
from ...models.preview_platform_tenant_self_workflow_actions_body import PreviewPlatformTenantSelfWorkflowActionsBody
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: PreviewPlatformTenantSelfWorkflowActionsBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/workflow-actions/preview",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowActionPreviewResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowActionPreviewResponse.from_dict(response.json())

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
) -> Response[OperationWorkflowActionPreviewResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PreviewPlatformTenantSelfWorkflowActionsBody,
) -> Response[OperationWorkflowActionPreviewResponse | Problem]:
    """Preview business workflow actions

     Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does
    not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation
    filter. Application must recheck locked rows and readiness before writing.

    Args:
        body (PreviewPlatformTenantSelfWorkflowActionsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowActionPreviewResponse | Problem]
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
    body: PreviewPlatformTenantSelfWorkflowActionsBody,
) -> OperationWorkflowActionPreviewResponse | Problem | None:
    """Preview business workflow actions

     Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does
    not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation
    filter. Application must recheck locked rows and readiness before writing.

    Args:
        body (PreviewPlatformTenantSelfWorkflowActionsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowActionPreviewResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PreviewPlatformTenantSelfWorkflowActionsBody,
) -> Response[OperationWorkflowActionPreviewResponse | Problem]:
    """Preview business workflow actions

     Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does
    not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation
    filter. Application must recheck locked rows and readiness before writing.

    Args:
        body (PreviewPlatformTenantSelfWorkflowActionsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowActionPreviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PreviewPlatformTenantSelfWorkflowActionsBody,
) -> OperationWorkflowActionPreviewResponse | Problem | None:
    """Preview business workflow actions

     Read-only candidates from the retained current state, evaluated with an empty evidence plan. Does
    not execute, authorize, reserve, or mutate an action. At most 100 actions; optional operation
    filter. Application must recheck locked rows and readiness before writing.

    Args:
        body (PreviewPlatformTenantSelfWorkflowActionsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowActionPreviewResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
