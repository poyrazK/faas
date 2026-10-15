from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_execution_lineage_case_backfill import EventExecutionLineageCaseBackfill
from ...models.problem import Problem
from ...models.request_event_execution_lineage_case_backfill_request import (
    RequestEventExecutionLineageCaseBackfillRequest,
)
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    body: RequestEventExecutionLineageCaseBackfillRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-reconciliation/case-backfill".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
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

    if response.status_code == 202:
        response_202 = EventExecutionLineageCaseBackfill.from_dict(response.json())

        return response_202

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
    *,
    client: AuthenticatedClient,
    body: RequestEventExecutionLineageCaseBackfillRequest,
) -> Response[EventExecutionLineageCaseBackfill | Problem]:
    """Backfill missing lineage cases from retained admission evidence.

     Requires deploy/write scope and an existing execution coverage row. The request UUID and expected
    previous scan UUID fence retries and replacement of a completed, cancelled or expired scan. Only the
    latest scan is retained in coverage metadata; superseded requests fail their stale guards. Captures
    an upper receipt ID and resumes a separate bounded cursor. Waits for active bulk lineage retries on
    this subscription. Inserts missing unresolved/conflicting diagnostics with unique diagnostic IDs,
    without overwriting existing cases, associating roots, executing handlers or reconstructing
    outcomes. Retained receipt membership remains live during the scan. Missing or pruned evidence
    cannot be recovered. Work expires 30 days after request; a deliberate replacement needs a new UUID
    and the latest request UUID as its guard.

    Args:
        slug (str):
        subscription_id (UUID):
        body (RequestEventExecutionLineageCaseBackfillRequest): Idempotent request identity and
            previous-scan guard for a bounded diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageCaseBackfill | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: RequestEventExecutionLineageCaseBackfillRequest,
) -> EventExecutionLineageCaseBackfill | Problem | None:
    """Backfill missing lineage cases from retained admission evidence.

     Requires deploy/write scope and an existing execution coverage row. The request UUID and expected
    previous scan UUID fence retries and replacement of a completed, cancelled or expired scan. Only the
    latest scan is retained in coverage metadata; superseded requests fail their stale guards. Captures
    an upper receipt ID and resumes a separate bounded cursor. Waits for active bulk lineage retries on
    this subscription. Inserts missing unresolved/conflicting diagnostics with unique diagnostic IDs,
    without overwriting existing cases, associating roots, executing handlers or reconstructing
    outcomes. Retained receipt membership remains live during the scan. Missing or pruned evidence
    cannot be recovered. Work expires 30 days after request; a deliberate replacement needs a new UUID
    and the latest request UUID as its guard.

    Args:
        slug (str):
        subscription_id (UUID):
        body (RequestEventExecutionLineageCaseBackfillRequest): Idempotent request identity and
            previous-scan guard for a bounded diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageCaseBackfill | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: RequestEventExecutionLineageCaseBackfillRequest,
) -> Response[EventExecutionLineageCaseBackfill | Problem]:
    """Backfill missing lineage cases from retained admission evidence.

     Requires deploy/write scope and an existing execution coverage row. The request UUID and expected
    previous scan UUID fence retries and replacement of a completed, cancelled or expired scan. Only the
    latest scan is retained in coverage metadata; superseded requests fail their stale guards. Captures
    an upper receipt ID and resumes a separate bounded cursor. Waits for active bulk lineage retries on
    this subscription. Inserts missing unresolved/conflicting diagnostics with unique diagnostic IDs,
    without overwriting existing cases, associating roots, executing handlers or reconstructing
    outcomes. Retained receipt membership remains live during the scan. Missing or pruned evidence
    cannot be recovered. Work expires 30 days after request; a deliberate replacement needs a new UUID
    and the latest request UUID as its guard.

    Args:
        slug (str):
        subscription_id (UUID):
        body (RequestEventExecutionLineageCaseBackfillRequest): Idempotent request identity and
            previous-scan guard for a bounded diagnostic backfill.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageCaseBackfill | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: RequestEventExecutionLineageCaseBackfillRequest,
) -> EventExecutionLineageCaseBackfill | Problem | None:
    """Backfill missing lineage cases from retained admission evidence.

     Requires deploy/write scope and an existing execution coverage row. The request UUID and expected
    previous scan UUID fence retries and replacement of a completed, cancelled or expired scan. Only the
    latest scan is retained in coverage metadata; superseded requests fail their stale guards. Captures
    an upper receipt ID and resumes a separate bounded cursor. Waits for active bulk lineage retries on
    this subscription. Inserts missing unresolved/conflicting diagnostics with unique diagnostic IDs,
    without overwriting existing cases, associating roots, executing handlers or reconstructing
    outcomes. Retained receipt membership remains live during the scan. Missing or pruned evidence
    cannot be recovered. Work expires 30 days after request; a deliberate replacement needs a new UUID
    and the latest request UUID as its guard.

    Args:
        slug (str):
        subscription_id (UUID):
        body (RequestEventExecutionLineageCaseBackfillRequest): Idempotent request identity and
            previous-scan guard for a bounded diagnostic backfill.

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
            client=client,
            body=body,
        )
    ).parsed
