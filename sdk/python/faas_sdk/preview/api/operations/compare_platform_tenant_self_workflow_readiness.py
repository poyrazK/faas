from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.compare_platform_tenant_self_workflow_readiness_body import (
    ComparePlatformTenantSelfWorkflowReadinessBody,
)
from ...models.operation_workflow_readiness_comparison_response import OperationWorkflowReadinessComparisonResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ComparePlatformTenantSelfWorkflowReadinessBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/workflow-readiness/compare",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessComparisonResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessComparisonResponse.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationWorkflowReadinessComparisonResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ComparePlatformTenantSelfWorkflowReadinessBody,
) -> Response[OperationWorkflowReadinessComparisonResponse | Problem]:
    """Compare customer transition readiness with a saved queue snapshot.

     Requires platform-tenant Operations read scope; ownership comes from authentication and overrides
    are rejected.
    This read-only comparison accepts a caller-held snapshot emitted by the queue.
    Snapshot scope binds the account, app, environment, customer selector and role.
    Baseline data is advisory and is not authenticated historical evidence or execution authority.
    At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
    Each distinct saved workflow is reread once with readiness-only projections,
    independent of recent queue sampling and current owner or requirement filters.
    The exact Operation/from/to edge and declared requirements must remain comparable.
    Contract changes, terminal or unavailable state, unavailable edges and older retained
    revisions are counted separately and never classified as newly ready or newly blocked.
    Comparisons provide added and resolved requirement codes plus changed blocker,
    prerequisite, pending-verification and reporting evidence categories.
    Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
    Change filters and the row limit apply after comparison; counts cover all saved transitions.
    The returned snapshot includes available current edges before filtering and can be saved
    as the next baseline. It omits unavailable edges. Reads are live and not atomic.
    Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
    not the complete sampled backlog. Applications retain authorization and execution.

    Args:
        body (ComparePlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessComparisonResponse | Problem]
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
    body: ComparePlatformTenantSelfWorkflowReadinessBody,
) -> OperationWorkflowReadinessComparisonResponse | Problem | None:
    """Compare customer transition readiness with a saved queue snapshot.

     Requires platform-tenant Operations read scope; ownership comes from authentication and overrides
    are rejected.
    This read-only comparison accepts a caller-held snapshot emitted by the queue.
    Snapshot scope binds the account, app, environment, customer selector and role.
    Baseline data is advisory and is not authenticated historical evidence or execution authority.
    At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
    Each distinct saved workflow is reread once with readiness-only projections,
    independent of recent queue sampling and current owner or requirement filters.
    The exact Operation/from/to edge and declared requirements must remain comparable.
    Contract changes, terminal or unavailable state, unavailable edges and older retained
    revisions are counted separately and never classified as newly ready or newly blocked.
    Comparisons provide added and resolved requirement codes plus changed blocker,
    prerequisite, pending-verification and reporting evidence categories.
    Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
    Change filters and the row limit apply after comparison; counts cover all saved transitions.
    The returned snapshot includes available current edges before filtering and can be saved
    as the next baseline. It omits unavailable edges. Reads are live and not atomic.
    Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
    not the complete sampled backlog. Applications retain authorization and execution.

    Args:
        body (ComparePlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessComparisonResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ComparePlatformTenantSelfWorkflowReadinessBody,
) -> Response[OperationWorkflowReadinessComparisonResponse | Problem]:
    """Compare customer transition readiness with a saved queue snapshot.

     Requires platform-tenant Operations read scope; ownership comes from authentication and overrides
    are rejected.
    This read-only comparison accepts a caller-held snapshot emitted by the queue.
    Snapshot scope binds the account, app, environment, customer selector and role.
    Baseline data is advisory and is not authenticated historical evidence or execution authority.
    At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
    Each distinct saved workflow is reread once with readiness-only projections,
    independent of recent queue sampling and current owner or requirement filters.
    The exact Operation/from/to edge and declared requirements must remain comparable.
    Contract changes, terminal or unavailable state, unavailable edges and older retained
    revisions are counted separately and never classified as newly ready or newly blocked.
    Comparisons provide added and resolved requirement codes plus changed blocker,
    prerequisite, pending-verification and reporting evidence categories.
    Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
    Change filters and the row limit apply after comparison; counts cover all saved transitions.
    The returned snapshot includes available current edges before filtering and can be saved
    as the next baseline. It omits unavailable edges. Reads are live and not atomic.
    Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
    not the complete sampled backlog. Applications retain authorization and execution.

    Args:
        body (ComparePlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessComparisonResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ComparePlatformTenantSelfWorkflowReadinessBody,
) -> OperationWorkflowReadinessComparisonResponse | Problem | None:
    """Compare customer transition readiness with a saved queue snapshot.

     Requires platform-tenant Operations read scope; ownership comes from authentication and overrides
    are rejected.
    This read-only comparison accepts a caller-held snapshot emitted by the queue.
    Snapshot scope binds the account, app, environment, customer selector and role.
    Baseline data is advisory and is not authenticated historical evidence or execution authority.
    At most 100 saved transitions are accepted in a body of at most 1048576 bytes.
    Each distinct saved workflow is reread once with readiness-only projections,
    independent of recent queue sampling and current owner or requirement filters.
    The exact Operation/from/to edge and declared requirements must remain comparable.
    Contract changes, terminal or unavailable state, unavailable edges and older retained
    revisions are counted separately and never classified as newly ready or newly blocked.
    Comparisons provide added and resolved requirement codes plus changed blocker,
    prerequisite, pending-verification and reporting evidence categories.
    Age counters alone are excluded from evidence fingerprints; freshness expiry can change readiness.
    Change filters and the row limit apply after comparison; counts cover all saved transitions.
    The returned snapshot includes available current edges before filtering and can be saved
    as the next baseline. It omits unavailable edges. Reads are live and not atomic.
    Empty snapshots are valid. Snapshot rows are limited to previously returned queue rows,
    not the complete sampled backlog. Applications retain authorization and execution.

    Args:
        body (ComparePlatformTenantSelfWorkflowReadinessBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessComparisonResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
