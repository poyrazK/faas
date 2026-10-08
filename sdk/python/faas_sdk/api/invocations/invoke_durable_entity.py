from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_invoke_request import DurableEntityInvokeRequest
from ...models.durable_entity_invoke_response import DurableEntityInvokeResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DurableEntityInvokeRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/entities/invoke".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityInvokeResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityInvokeResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DurableEntityInvokeResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityInvokeRequest,
) -> Response[DurableEntityInvokeResponse | Problem]:
    """Invoke an object-storage-backed entity in the operator preview.

     Disabled unless the operator configures a private bucket and explicitly
    enables this app. Account authentication, deploy-write scopes and MFA
    apply. A selected customer must belong to the account and be active;
    customer self-service tokens are not accepted on this surface.
    Entity identity includes the app, immutable environment identity,
    optional verified customer, namespace and key. The deployed handler
    receives state at POST /__gregale/entities and returns data, result and
    an optional alarm_at. Handlers must compute transitions without external
    side effects. Alarm delivery requires a separate operator opt-in and
    private delimiter listing. Due alarms use the same scheduler path with
    event=alarm. Clearing or replacing the deadline invalidates stale work;
    a failed handler leaves the alarm due. Attempts may repeat, while state,
    the alarm receipt and its next deadline publish atomically. Alarm timing
    depends on bounded entity sweeps and has no production latency guarantee.
    State and replay receipts commit in object storage; the existing SQL
    invocation ledger is used only to schedule and observe guest execution.
    Keep request_id and exact payload bytes for retries, including after an
    uncertain response. HTTP Idempotency-Key does not identify entity work.
    Request IDs starting with __gregale_alarm/ are reserved for delivery.
    Replays return the original result and version without executing code.
    Calls have a 25 second budget. Owners expire after at most five minutes.
    An immutable receipt index preserves original results without a receipt
    count ceiling. Encoded snapshots and individual receipts remain bounded
    to 1 MiB. Operator cleanup can reclaim superseded snapshots and index
    nodes after a fenced generation barrier; it never expires replay receipts.
    An explicit operator per-entity byte cap can limit the current snapshot
    and its reachable immutable receipts/index/archive. It excludes metadata,
    abandoned uploads and provider history; it is not a plan billing quota.
    Over-cap new work returns 409 durable_entity_storage_limit with limit and
    observed projected bytes. Existing receipts replay at capacity. Legacy
    entities under a cap return 503 durable_entity_inventory_pending for new
    work until bounded verified accounting completes. Handler computation
    can run before quota rejection. Entity deletion is not available yet.

    Args:
        slug (str):
        body (DurableEntityInvokeRequest): Preview entity invocation; request_id and payload
            identify durable retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityInvokeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityInvokeRequest,
) -> DurableEntityInvokeResponse | Problem | None:
    """Invoke an object-storage-backed entity in the operator preview.

     Disabled unless the operator configures a private bucket and explicitly
    enables this app. Account authentication, deploy-write scopes and MFA
    apply. A selected customer must belong to the account and be active;
    customer self-service tokens are not accepted on this surface.
    Entity identity includes the app, immutable environment identity,
    optional verified customer, namespace and key. The deployed handler
    receives state at POST /__gregale/entities and returns data, result and
    an optional alarm_at. Handlers must compute transitions without external
    side effects. Alarm delivery requires a separate operator opt-in and
    private delimiter listing. Due alarms use the same scheduler path with
    event=alarm. Clearing or replacing the deadline invalidates stale work;
    a failed handler leaves the alarm due. Attempts may repeat, while state,
    the alarm receipt and its next deadline publish atomically. Alarm timing
    depends on bounded entity sweeps and has no production latency guarantee.
    State and replay receipts commit in object storage; the existing SQL
    invocation ledger is used only to schedule and observe guest execution.
    Keep request_id and exact payload bytes for retries, including after an
    uncertain response. HTTP Idempotency-Key does not identify entity work.
    Request IDs starting with __gregale_alarm/ are reserved for delivery.
    Replays return the original result and version without executing code.
    Calls have a 25 second budget. Owners expire after at most five minutes.
    An immutable receipt index preserves original results without a receipt
    count ceiling. Encoded snapshots and individual receipts remain bounded
    to 1 MiB. Operator cleanup can reclaim superseded snapshots and index
    nodes after a fenced generation barrier; it never expires replay receipts.
    An explicit operator per-entity byte cap can limit the current snapshot
    and its reachable immutable receipts/index/archive. It excludes metadata,
    abandoned uploads and provider history; it is not a plan billing quota.
    Over-cap new work returns 409 durable_entity_storage_limit with limit and
    observed projected bytes. Existing receipts replay at capacity. Legacy
    entities under a cap return 503 durable_entity_inventory_pending for new
    work until bounded verified accounting completes. Handler computation
    can run before quota rejection. Entity deletion is not available yet.

    Args:
        slug (str):
        body (DurableEntityInvokeRequest): Preview entity invocation; request_id and payload
            identify durable retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityInvokeResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityInvokeRequest,
) -> Response[DurableEntityInvokeResponse | Problem]:
    """Invoke an object-storage-backed entity in the operator preview.

     Disabled unless the operator configures a private bucket and explicitly
    enables this app. Account authentication, deploy-write scopes and MFA
    apply. A selected customer must belong to the account and be active;
    customer self-service tokens are not accepted on this surface.
    Entity identity includes the app, immutable environment identity,
    optional verified customer, namespace and key. The deployed handler
    receives state at POST /__gregale/entities and returns data, result and
    an optional alarm_at. Handlers must compute transitions without external
    side effects. Alarm delivery requires a separate operator opt-in and
    private delimiter listing. Due alarms use the same scheduler path with
    event=alarm. Clearing or replacing the deadline invalidates stale work;
    a failed handler leaves the alarm due. Attempts may repeat, while state,
    the alarm receipt and its next deadline publish atomically. Alarm timing
    depends on bounded entity sweeps and has no production latency guarantee.
    State and replay receipts commit in object storage; the existing SQL
    invocation ledger is used only to schedule and observe guest execution.
    Keep request_id and exact payload bytes for retries, including after an
    uncertain response. HTTP Idempotency-Key does not identify entity work.
    Request IDs starting with __gregale_alarm/ are reserved for delivery.
    Replays return the original result and version without executing code.
    Calls have a 25 second budget. Owners expire after at most five minutes.
    An immutable receipt index preserves original results without a receipt
    count ceiling. Encoded snapshots and individual receipts remain bounded
    to 1 MiB. Operator cleanup can reclaim superseded snapshots and index
    nodes after a fenced generation barrier; it never expires replay receipts.
    An explicit operator per-entity byte cap can limit the current snapshot
    and its reachable immutable receipts/index/archive. It excludes metadata,
    abandoned uploads and provider history; it is not a plan billing quota.
    Over-cap new work returns 409 durable_entity_storage_limit with limit and
    observed projected bytes. Existing receipts replay at capacity. Legacy
    entities under a cap return 503 durable_entity_inventory_pending for new
    work until bounded verified accounting completes. Handler computation
    can run before quota rejection. Entity deletion is not available yet.

    Args:
        slug (str):
        body (DurableEntityInvokeRequest): Preview entity invocation; request_id and payload
            identify durable retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityInvokeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DurableEntityInvokeRequest,
) -> DurableEntityInvokeResponse | Problem | None:
    """Invoke an object-storage-backed entity in the operator preview.

     Disabled unless the operator configures a private bucket and explicitly
    enables this app. Account authentication, deploy-write scopes and MFA
    apply. A selected customer must belong to the account and be active;
    customer self-service tokens are not accepted on this surface.
    Entity identity includes the app, immutable environment identity,
    optional verified customer, namespace and key. The deployed handler
    receives state at POST /__gregale/entities and returns data, result and
    an optional alarm_at. Handlers must compute transitions without external
    side effects. Alarm delivery requires a separate operator opt-in and
    private delimiter listing. Due alarms use the same scheduler path with
    event=alarm. Clearing or replacing the deadline invalidates stale work;
    a failed handler leaves the alarm due. Attempts may repeat, while state,
    the alarm receipt and its next deadline publish atomically. Alarm timing
    depends on bounded entity sweeps and has no production latency guarantee.
    State and replay receipts commit in object storage; the existing SQL
    invocation ledger is used only to schedule and observe guest execution.
    Keep request_id and exact payload bytes for retries, including after an
    uncertain response. HTTP Idempotency-Key does not identify entity work.
    Request IDs starting with __gregale_alarm/ are reserved for delivery.
    Replays return the original result and version without executing code.
    Calls have a 25 second budget. Owners expire after at most five minutes.
    An immutable receipt index preserves original results without a receipt
    count ceiling. Encoded snapshots and individual receipts remain bounded
    to 1 MiB. Operator cleanup can reclaim superseded snapshots and index
    nodes after a fenced generation barrier; it never expires replay receipts.
    An explicit operator per-entity byte cap can limit the current snapshot
    and its reachable immutable receipts/index/archive. It excludes metadata,
    abandoned uploads and provider history; it is not a plan billing quota.
    Over-cap new work returns 409 durable_entity_storage_limit with limit and
    observed projected bytes. Existing receipts replay at capacity. Legacy
    entities under a cap return 503 durable_entity_inventory_pending for new
    work until bounded verified accounting completes. Handler computation
    can run before quota rejection. Entity deletion is not available yet.

    Args:
        slug (str):
        body (DurableEntityInvokeRequest): Preview entity invocation; request_id and payload
            identify durable retries.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityInvokeResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
