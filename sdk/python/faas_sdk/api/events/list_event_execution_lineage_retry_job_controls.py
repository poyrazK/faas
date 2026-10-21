import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_execution_lineage_retry_job_controls import EventExecutionLineageRetryJobControls
from ...models.list_event_execution_lineage_retry_job_controls_action import (
    ListEventExecutionLineageRetryJobControlsAction,
)
from ...models.list_event_execution_lineage_retry_job_controls_credential_type import (
    ListEventExecutionLineageRetryJobControlsCredentialType,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    expected_created_at: datetime.datetime,
    after: int | Unset = 0,
    limit: int | Unset = 50,
    action: ListEventExecutionLineageRetryJobControlsAction | Unset = UNSET,
    actor_id: str | Unset = UNSET,
    credential_type: ListEventExecutionLineageRetryJobControlsCredentialType | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_expected_created_at = expected_created_at.isoformat()
    params["expected_created_at"] = json_expected_created_at

    params["after"] = after

    params["limit"] = limit

    json_action: str | Unset = UNSET
    if not isinstance(action, Unset):
        json_action = action

    params["action"] = json_action

    params["actor_id"] = actor_id

    json_credential_type: str | Unset = UNSET
    if not isinstance(credential_type, Unset):
        json_credential_type = credential_type

    params["credential_type"] = json_credential_type

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-reconciliation/retry-jobs/{request_id}/controls".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventExecutionLineageRetryJobControls | Problem | None:
    if response.status_code == 200:
        response_200 = EventExecutionLineageRetryJobControls.from_dict(response.json())

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
) -> Response[EventExecutionLineageRetryJobControls | Problem]:
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
    expected_created_at: datetime.datetime,
    after: int | Unset = 0,
    limit: int | Unset = 50,
    action: ListEventExecutionLineageRetryJobControlsAction | Unset = UNSET,
    actor_id: str | Unset = UNSET,
    credential_type: ListEventExecutionLineageRetryJobControlsCredentialType | Unset = UNSET,
) -> Response[EventExecutionLineageRetryJobControls | Problem]:
    """List retained immutable bulk retry control receipts.

     Requires read scope and MFA. Fences the original job incarnation with expected_created_at.
    Successful UUID-keyed pause/resume/cancel commands, including new-command no-ops, are ordered by
    their increasing per-job sequence. Duplicate commands create no new receipt. Legacy controls without
    a UUID and failed requests are absent. New receipts include server-derived actor_kind, actor_id and
    credential_type; older receipts omit attribution. Repeats preserve the original actor even under
    another authorized credential. Receipts are immutable metadata observations, expire with the
    original job and cascade on job deletion. Optional action, actor_id and credential_type filters
    combine with AND before pagination. Sequence cursors retain their original per-job meaning and may
    have gaps; keep filters unchanged across pages or restart at after=0. Legacy unattributed receipts
    remain in unfiltered history and match action filters but not nonempty attribution filters.
    Pagination is a live append-only listing, not a reservation; timestamps are informational and
    sequence defines order. An existing retained job with no receipts returns an empty controls array.
    Expired/missing jobs return 404 and stale creation guards return 409.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        expected_created_at (datetime.datetime):
        after (int | Unset):  Default: 0.
        limit (int | Unset): Zero selects the default page size. Context: List retained immutable
            bulk retry control receipts. Default: 50.
        action (ListEventExecutionLineageRetryJobControlsAction | Unset):
        actor_id (str | Unset):
        credential_type (ListEventExecutionLineageRetryJobControlsCredentialType | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageRetryJobControls | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        expected_created_at=expected_created_at,
        after=after,
        limit=limit,
        action=action,
        actor_id=actor_id,
        credential_type=credential_type,
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
    expected_created_at: datetime.datetime,
    after: int | Unset = 0,
    limit: int | Unset = 50,
    action: ListEventExecutionLineageRetryJobControlsAction | Unset = UNSET,
    actor_id: str | Unset = UNSET,
    credential_type: ListEventExecutionLineageRetryJobControlsCredentialType | Unset = UNSET,
) -> EventExecutionLineageRetryJobControls | Problem | None:
    """List retained immutable bulk retry control receipts.

     Requires read scope and MFA. Fences the original job incarnation with expected_created_at.
    Successful UUID-keyed pause/resume/cancel commands, including new-command no-ops, are ordered by
    their increasing per-job sequence. Duplicate commands create no new receipt. Legacy controls without
    a UUID and failed requests are absent. New receipts include server-derived actor_kind, actor_id and
    credential_type; older receipts omit attribution. Repeats preserve the original actor even under
    another authorized credential. Receipts are immutable metadata observations, expire with the
    original job and cascade on job deletion. Optional action, actor_id and credential_type filters
    combine with AND before pagination. Sequence cursors retain their original per-job meaning and may
    have gaps; keep filters unchanged across pages or restart at after=0. Legacy unattributed receipts
    remain in unfiltered history and match action filters but not nonempty attribution filters.
    Pagination is a live append-only listing, not a reservation; timestamps are informational and
    sequence defines order. An existing retained job with no receipts returns an empty controls array.
    Expired/missing jobs return 404 and stale creation guards return 409.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        expected_created_at (datetime.datetime):
        after (int | Unset):  Default: 0.
        limit (int | Unset): Zero selects the default page size. Context: List retained immutable
            bulk retry control receipts. Default: 50.
        action (ListEventExecutionLineageRetryJobControlsAction | Unset):
        actor_id (str | Unset):
        credential_type (ListEventExecutionLineageRetryJobControlsCredentialType | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageRetryJobControls | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        client=client,
        expected_created_at=expected_created_at,
        after=after,
        limit=limit,
        action=action,
        actor_id=actor_id,
        credential_type=credential_type,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    expected_created_at: datetime.datetime,
    after: int | Unset = 0,
    limit: int | Unset = 50,
    action: ListEventExecutionLineageRetryJobControlsAction | Unset = UNSET,
    actor_id: str | Unset = UNSET,
    credential_type: ListEventExecutionLineageRetryJobControlsCredentialType | Unset = UNSET,
) -> Response[EventExecutionLineageRetryJobControls | Problem]:
    """List retained immutable bulk retry control receipts.

     Requires read scope and MFA. Fences the original job incarnation with expected_created_at.
    Successful UUID-keyed pause/resume/cancel commands, including new-command no-ops, are ordered by
    their increasing per-job sequence. Duplicate commands create no new receipt. Legacy controls without
    a UUID and failed requests are absent. New receipts include server-derived actor_kind, actor_id and
    credential_type; older receipts omit attribution. Repeats preserve the original actor even under
    another authorized credential. Receipts are immutable metadata observations, expire with the
    original job and cascade on job deletion. Optional action, actor_id and credential_type filters
    combine with AND before pagination. Sequence cursors retain their original per-job meaning and may
    have gaps; keep filters unchanged across pages or restart at after=0. Legacy unattributed receipts
    remain in unfiltered history and match action filters but not nonempty attribution filters.
    Pagination is a live append-only listing, not a reservation; timestamps are informational and
    sequence defines order. An existing retained job with no receipts returns an empty controls array.
    Expired/missing jobs return 404 and stale creation guards return 409.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        expected_created_at (datetime.datetime):
        after (int | Unset):  Default: 0.
        limit (int | Unset): Zero selects the default page size. Context: List retained immutable
            bulk retry control receipts. Default: 50.
        action (ListEventExecutionLineageRetryJobControlsAction | Unset):
        actor_id (str | Unset):
        credential_type (ListEventExecutionLineageRetryJobControlsCredentialType | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageRetryJobControls | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        request_id=request_id,
        expected_created_at=expected_created_at,
        after=after,
        limit=limit,
        action=action,
        actor_id=actor_id,
        credential_type=credential_type,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
    expected_created_at: datetime.datetime,
    after: int | Unset = 0,
    limit: int | Unset = 50,
    action: ListEventExecutionLineageRetryJobControlsAction | Unset = UNSET,
    actor_id: str | Unset = UNSET,
    credential_type: ListEventExecutionLineageRetryJobControlsCredentialType | Unset = UNSET,
) -> EventExecutionLineageRetryJobControls | Problem | None:
    """List retained immutable bulk retry control receipts.

     Requires read scope and MFA. Fences the original job incarnation with expected_created_at.
    Successful UUID-keyed pause/resume/cancel commands, including new-command no-ops, are ordered by
    their increasing per-job sequence. Duplicate commands create no new receipt. Legacy controls without
    a UUID and failed requests are absent. New receipts include server-derived actor_kind, actor_id and
    credential_type; older receipts omit attribution. Repeats preserve the original actor even under
    another authorized credential. Receipts are immutable metadata observations, expire with the
    original job and cascade on job deletion. Optional action, actor_id and credential_type filters
    combine with AND before pagination. Sequence cursors retain their original per-job meaning and may
    have gaps; keep filters unchanged across pages or restart at after=0. Legacy unattributed receipts
    remain in unfiltered history and match action filters but not nonempty attribution filters.
    Pagination is a live append-only listing, not a reservation; timestamps are informational and
    sequence defines order. An existing retained job with no receipts returns an empty controls array.
    Expired/missing jobs return 404 and stale creation guards return 409.

    Args:
        slug (str):
        subscription_id (UUID):
        request_id (UUID):
        expected_created_at (datetime.datetime):
        after (int | Unset):  Default: 0.
        limit (int | Unset): Zero selects the default page size. Context: List retained immutable
            bulk retry control receipts. Default: 50.
        action (ListEventExecutionLineageRetryJobControlsAction | Unset):
        actor_id (str | Unset):
        credential_type (ListEventExecutionLineageRetryJobControlsCredentialType | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageRetryJobControls | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            request_id=request_id,
            client=client,
            expected_created_at=expected_created_at,
            after=after,
            limit=limit,
            action=action,
            actor_id=actor_id,
            credential_type=credential_type,
        )
    ).parsed
