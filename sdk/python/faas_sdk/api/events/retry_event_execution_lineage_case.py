from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_execution_lineage_case import EventExecutionLineageCase
from ...models.problem import Problem
from ...models.retry_event_execution_lineage_case_request import RetryEventExecutionLineageCaseRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    outbox_id: int,
    *,
    body: RetryEventExecutionLineageCaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-reconciliation/cases/{outbox_id}/retry".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
            outbox_id=quote(str(outbox_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventExecutionLineageCase | Problem | None:
    if response.status_code == 200:
        response_200 = EventExecutionLineageCase.from_dict(response.json())

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

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventExecutionLineageCase | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    outbox_id: int,
    *,
    client: AuthenticatedClient,
    body: RetryEventExecutionLineageCaseRequest,
) -> Response[EventExecutionLineageCase | Problem]:
    """Recheck one reviewed lineage case with an idempotent guard.

     Requires deploy/write scope. Request UUID, expected revision and any diagnostic identity fence each
    evaluation; duplicate requests return the original result for 30 days. Reuses authoritative evidence
    checks and does not reset scan progress or established coverage. Known receipt acceptance and root
    incarnation remain guarded. No event payloads or handler errors are returned.

    Args:
        slug (str):
        subscription_id (UUID):
        outbox_id (int):
        body (RetryEventExecutionLineageCaseRequest): Idempotent reevaluation identity and
            reviewed revision guard for a single lineage case.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageCase | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        outbox_id=outbox_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    outbox_id: int,
    *,
    client: AuthenticatedClient,
    body: RetryEventExecutionLineageCaseRequest,
) -> EventExecutionLineageCase | Problem | None:
    """Recheck one reviewed lineage case with an idempotent guard.

     Requires deploy/write scope. Request UUID, expected revision and any diagnostic identity fence each
    evaluation; duplicate requests return the original result for 30 days. Reuses authoritative evidence
    checks and does not reset scan progress or established coverage. Known receipt acceptance and root
    incarnation remain guarded. No event payloads or handler errors are returned.

    Args:
        slug (str):
        subscription_id (UUID):
        outbox_id (int):
        body (RetryEventExecutionLineageCaseRequest): Idempotent reevaluation identity and
            reviewed revision guard for a single lineage case.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageCase | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        outbox_id=outbox_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    outbox_id: int,
    *,
    client: AuthenticatedClient,
    body: RetryEventExecutionLineageCaseRequest,
) -> Response[EventExecutionLineageCase | Problem]:
    """Recheck one reviewed lineage case with an idempotent guard.

     Requires deploy/write scope. Request UUID, expected revision and any diagnostic identity fence each
    evaluation; duplicate requests return the original result for 30 days. Reuses authoritative evidence
    checks and does not reset scan progress or established coverage. Known receipt acceptance and root
    incarnation remain guarded. No event payloads or handler errors are returned.

    Args:
        slug (str):
        subscription_id (UUID):
        outbox_id (int):
        body (RetryEventExecutionLineageCaseRequest): Idempotent reevaluation identity and
            reviewed revision guard for a single lineage case.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageCase | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        outbox_id=outbox_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    outbox_id: int,
    *,
    client: AuthenticatedClient,
    body: RetryEventExecutionLineageCaseRequest,
) -> EventExecutionLineageCase | Problem | None:
    """Recheck one reviewed lineage case with an idempotent guard.

     Requires deploy/write scope. Request UUID, expected revision and any diagnostic identity fence each
    evaluation; duplicate requests return the original result for 30 days. Reuses authoritative evidence
    checks and does not reset scan progress or established coverage. Known receipt acceptance and root
    incarnation remain guarded. No event payloads or handler errors are returned.

    Args:
        slug (str):
        subscription_id (UUID):
        outbox_id (int):
        body (RetryEventExecutionLineageCaseRequest): Idempotent reevaluation identity and
            reviewed revision guard for a single lineage case.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageCase | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            outbox_id=outbox_id,
            client=client,
            body=body,
        )
    ).parsed
