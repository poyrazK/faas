from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.control_event_execution_lineage_case_backfill_request import (
    ControlEventExecutionLineageCaseBackfillRequest,
)
from ...models.event_execution_lineage_case_backfill import EventExecutionLineageCaseBackfill
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    body: ControlEventExecutionLineageCaseBackfillRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-reconciliation/case-backfill/{request_id}/resume".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventExecutionLineageCaseBackfill | Problem | None:
    if response.status_code == 200:
        response_200 = EventExecutionLineageCaseBackfill.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventExecutionLineageCaseBackfill | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ControlEventExecutionLineageCaseBackfillRequest,
) -> Response[EventExecutionLineageCaseBackfill | Problem]:
    """Resume a retained lineage diagnostic scan.

     Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation.
    Resumes a paused scan from its saved cursor and fixed retained range. Repeating resume on a pending
    or waiting scan leaves metadata unchanged. Completed, cancelled and expired scans return 409. Active
    bulk retry jobs still govern whether the scheduler can scan. Controls serialize under the same
    coverage lock as scheduler batches; an already-held batch may commit before the control. Range,
    counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged.
    Paused scans expire on their original deadline and cannot be replaced until cancelled or expired.
    These are desired-state controls, not an immutable command ledger; conflicting controls follow
    server lock order. Read status before retrying after an intervening opposite control.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (ControlEventExecutionLineageCaseBackfillRequest): Original scan creation guard for
            pausing, resuming, or cancelling a retained diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageCaseBackfill | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ControlEventExecutionLineageCaseBackfillRequest,
) -> EventExecutionLineageCaseBackfill | Problem | None:
    """Resume a retained lineage diagnostic scan.

     Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation.
    Resumes a paused scan from its saved cursor and fixed retained range. Repeating resume on a pending
    or waiting scan leaves metadata unchanged. Completed, cancelled and expired scans return 409. Active
    bulk retry jobs still govern whether the scheduler can scan. Controls serialize under the same
    coverage lock as scheduler batches; an already-held batch may commit before the control. Range,
    counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged.
    Paused scans expire on their original deadline and cannot be replaced until cancelled or expired.
    These are desired-state controls, not an immutable command ledger; conflicting controls follow
    server lock order. Read status before retrying after an intervening opposite control.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (ControlEventExecutionLineageCaseBackfillRequest): Original scan creation guard for
            pausing, resuming, or cancelling a retained diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageCaseBackfill | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ControlEventExecutionLineageCaseBackfillRequest,
) -> Response[EventExecutionLineageCaseBackfill | Problem]:
    """Resume a retained lineage diagnostic scan.

     Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation.
    Resumes a paused scan from its saved cursor and fixed retained range. Repeating resume on a pending
    or waiting scan leaves metadata unchanged. Completed, cancelled and expired scans return 409. Active
    bulk retry jobs still govern whether the scheduler can scan. Controls serialize under the same
    coverage lock as scheduler batches; an already-held batch may commit before the control. Range,
    counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged.
    Paused scans expire on their original deadline and cannot be replaced until cancelled or expired.
    These are desired-state controls, not an immutable command ledger; conflicting controls follow
    server lock order. Read status before retrying after an intervening opposite control.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (ControlEventExecutionLineageCaseBackfillRequest): Original scan creation guard for
            pausing, resuming, or cancelling a retained diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageCaseBackfill | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ControlEventExecutionLineageCaseBackfillRequest,
) -> EventExecutionLineageCaseBackfill | Problem | None:
    """Resume a retained lineage diagnostic scan.

     Requires deploy/write scope and MFA. Scan UUID and original created_at fence the incarnation.
    Resumes a paused scan from its saved cursor and fixed retained range. Repeating resume on a pending
    or waiting scan leaves metadata unchanged. Completed, cancelled and expired scans return 409. Active
    bulk retry jobs still govern whether the scheduler can scan. Controls serialize under the same
    coverage lock as scheduler batches; an already-held batch may commit before the control. Range,
    counters, existing diagnostics, normal repair progress and original 30-day deadline stay unchanged.
    Paused scans expire on their original deadline and cannot be replaced until cancelled or expired.
    These are desired-state controls, not an immutable command ledger; conflicting controls follow
    server lock order. Read status before retrying after an intervening opposite control.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        body (ControlEventExecutionLineageCaseBackfillRequest): Original scan creation guard for
            pausing, resuming, or cancelling a retained diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageCaseBackfill | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            request_id=request_id,
            client=client,
            body=body,
        )
    ).parsed
