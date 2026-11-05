from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.tenant_workflow_schedule_response import TenantWorkflowScheduleResponse
from ...models.update_tenant_workflow_schedule_request import UpdateTenantWorkflowScheduleRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: UpdateTenantWorkflowScheduleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/platform-tenant-self/apps/{slug}/workflows/schedules/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TenantWorkflowScheduleResponse | None:
    if response.status_code == 200:
        response_200 = TenantWorkflowScheduleResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Problem | TenantWorkflowScheduleResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: UpdateTenantWorkflowScheduleRequest,
) -> Response[Problem | TenantWorkflowScheduleResponse]:
    """Set this tenant's cadence for an opted-in workflow.

     Requires platform_tenant:automations:manage. expected_version is zero
    to create a tenant override and otherwise must match the current version.
    A stale version returns 409. Omitting timezone or overlap keeps the
    published defaults; enabled defaults to true. The tenant controls only
    its own cadence, timezone, overlap behavior, and enabled state. Input,
    workflow steps, credentials, and app concurrency remain app-owned.
    Existing runs keep their admitted definition.

    Args:
        slug (str):
        name (str):
        body (UpdateTenantWorkflowScheduleRequest): Tenant-owned schedule settings. Workflow input
            and workflow steps cannot be changed here.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TenantWorkflowScheduleResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: UpdateTenantWorkflowScheduleRequest,
) -> Problem | TenantWorkflowScheduleResponse | None:
    """Set this tenant's cadence for an opted-in workflow.

     Requires platform_tenant:automations:manage. expected_version is zero
    to create a tenant override and otherwise must match the current version.
    A stale version returns 409. Omitting timezone or overlap keeps the
    published defaults; enabled defaults to true. The tenant controls only
    its own cadence, timezone, overlap behavior, and enabled state. Input,
    workflow steps, credentials, and app concurrency remain app-owned.
    Existing runs keep their admitted definition.

    Args:
        slug (str):
        name (str):
        body (UpdateTenantWorkflowScheduleRequest): Tenant-owned schedule settings. Workflow input
            and workflow steps cannot be changed here.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TenantWorkflowScheduleResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: UpdateTenantWorkflowScheduleRequest,
) -> Response[Problem | TenantWorkflowScheduleResponse]:
    """Set this tenant's cadence for an opted-in workflow.

     Requires platform_tenant:automations:manage. expected_version is zero
    to create a tenant override and otherwise must match the current version.
    A stale version returns 409. Omitting timezone or overlap keeps the
    published defaults; enabled defaults to true. The tenant controls only
    its own cadence, timezone, overlap behavior, and enabled state. Input,
    workflow steps, credentials, and app concurrency remain app-owned.
    Existing runs keep their admitted definition.

    Args:
        slug (str):
        name (str):
        body (UpdateTenantWorkflowScheduleRequest): Tenant-owned schedule settings. Workflow input
            and workflow steps cannot be changed here.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TenantWorkflowScheduleResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    body: UpdateTenantWorkflowScheduleRequest,
) -> Problem | TenantWorkflowScheduleResponse | None:
    """Set this tenant's cadence for an opted-in workflow.

     Requires platform_tenant:automations:manage. expected_version is zero
    to create a tenant override and otherwise must match the current version.
    A stale version returns 409. Omitting timezone or overlap keeps the
    published defaults; enabled defaults to true. The tenant controls only
    its own cadence, timezone, overlap behavior, and enabled state. Input,
    workflow steps, credentials, and app concurrency remain app-owned.
    Existing runs keep their admitted definition.

    Args:
        slug (str):
        name (str):
        body (UpdateTenantWorkflowScheduleRequest): Tenant-owned schedule settings. Workflow input
            and workflow steps cannot be changed here.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TenantWorkflowScheduleResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
