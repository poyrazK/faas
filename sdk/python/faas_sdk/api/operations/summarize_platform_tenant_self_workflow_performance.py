from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_performance_summary import OperationWorkflowPerformanceSummary
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    workflow: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["workflow"] = workflow

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-performance/summary",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowPerformanceSummary | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowPerformanceSummary.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowPerformanceSummary | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
) -> Response[OperationWorkflowPerformanceSummary | Problem]:
    """Summarize business workflow performance across retained instances.

     Customer identity comes from credentials and tenant overrides are rejected. Requires
    platform_tenant:operations:read. Explicit app/environment/workflow selection is required. The latest
    100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all
    retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95
    percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per
    reported group. At most 1024 reports per instance and 32 output groups per dimension; group
    truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor
    or time-window filter is supported. Retention and newly published reports can change results.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPerformanceSummary | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
) -> OperationWorkflowPerformanceSummary | Problem | None:
    """Summarize business workflow performance across retained instances.

     Customer identity comes from credentials and tenant overrides are rejected. Requires
    platform_tenant:operations:read. Explicit app/environment/workflow selection is required. The latest
    100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all
    retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95
    percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per
    reported group. At most 1024 reports per instance and 32 output groups per dimension; group
    truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor
    or time-window filter is supported. Retention and newly published reports can change results.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPerformanceSummary | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        workflow=workflow,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
) -> Response[OperationWorkflowPerformanceSummary | Problem]:
    """Summarize business workflow performance across retained instances.

     Customer identity comes from credentials and tenant overrides are rejected. Requires
    platform_tenant:operations:read. Explicit app/environment/workflow selection is required. The latest
    100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all
    retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95
    percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per
    reported group. At most 1024 reports per instance and 32 output groups per dimension; group
    truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor
    or time-window filter is supported. Retention and newly published reports can change results.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowPerformanceSummary | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        workflow=workflow,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    workflow: str,
) -> OperationWorkflowPerformanceSummary | Problem | None:
    """Summarize business workflow performance across retained instances.

     Customer identity comes from credentials and tenant overrides are rejected. Requires
    platform_tenant:operations:read. Explicit app/environment/workflow selection is required. The latest
    100 completed and 100 ongoing retained instances form separate cohorts. Matching counts include all
    retained instances. Incomplete histories are excluded from durations and nearest-rank p50/p95
    percentiles with explicit coverage reasons. Each instance contributes one accumulated duration per
    reported group. At most 1024 reports per instance and 32 output groups per dimension; group
    truncation does not change cohort totals. Ongoing durations run through evaluation time. No cursor
    or time-window filter is supported. Retention and newly published reports can change results.

    Args:
        app_id (UUID):
        scope (str):
        workflow (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowPerformanceSummary | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            workflow=workflow,
        )
    ).parsed
