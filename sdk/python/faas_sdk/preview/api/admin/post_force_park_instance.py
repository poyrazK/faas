from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operator_intent_accepted_response import OperatorIntentAcceptedResponse
from ...models.post_force_park_instance_confirm import (
    PostForceParkInstanceConfirm,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    confirm: PostForceParkInstanceConfirm,
    reason: str | Unset = UNSET,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    json_confirm: str = confirm
    params["confirm"] = json_confirm

    params["reason"] = reason

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/instances/{id}/force-park".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperatorIntentAcceptedResponse | Problem | None:
    if response.status_code == 202:
        response_202 = OperatorIntentAcceptedResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperatorIntentAcceptedResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: PostForceParkInstanceConfirm,
    reason: str | Unset = UNSET,
    idempotency_key: str,
) -> Response[OperatorIntentAcceptedResponse | Problem]:
    """Enqueue a force-park intent for a wedged live instance (admin-only).

     Operator-side recovery primitive for instances wedged in
    {RUNNING, WAKING, COLD_BOOTING} that the customer can't
    wait for the idle reaper to handle. PR #1099 P2 redesign:
    apid writes a row to `operator_intents` (PR #1099 P2.1)
    and emits `pg_notify('operator_intent', …)`; schedd
    (the ONLY writer to `instances` per CLAUDE.md §6.2) is
    the sole consumer and dispatches via
    `engine.ParkWithReason` so the `pkg/state/machine.go`
    `CanTransition` guard fires. The handler returns 202
    Accepted with an intent_id; the operator polls
    GET /v1/admin/operator-intents/{id} for terminal state.

    `?confirm=true` is required as a tripwire against
    operator fat-fingering. Optional `?reason=<slug>` defaults
    to `operator_force_park`; values are clamped to the
    `[a-z0-9_]{1,64}` shape.

    ADR-1524: requires an MFA-verified operator session with step-up in
    the last five minutes, admin scope, the operator allowlist and same
    origin. Bearer keys do not satisfy step-up. Requires one
    Idempotency-Key (1-255 visible ASCII characters), an empty body,
    exactly one confirm=true and at most one reason; other query
    parameters are rejected. The key is scoped to the actor across all
    three recovery routes. Intent, exact 202 response and attributed
    request audit handoff commit atomically. Matching retries return
    the original response before reading mutable target state, without
    expiry; changed action, target or reason returns 409
    operator_submission_conflict. Trace changes do not change identity.
    An existing legacy recovery cache/reservation returns 409
    operator_submission_legacy and requires reconciliation. Notification
    after acceptance is best effort; the scheduler safety tick recovers
    a lost notification.

    Args:
        id (UUID):
        confirm (PostForceParkInstanceConfirm):
        reason (str | Unset):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorIntentAcceptedResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        confirm=confirm,
        reason=reason,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: PostForceParkInstanceConfirm,
    reason: str | Unset = UNSET,
    idempotency_key: str,
) -> OperatorIntentAcceptedResponse | Problem | None:
    """Enqueue a force-park intent for a wedged live instance (admin-only).

     Operator-side recovery primitive for instances wedged in
    {RUNNING, WAKING, COLD_BOOTING} that the customer can't
    wait for the idle reaper to handle. PR #1099 P2 redesign:
    apid writes a row to `operator_intents` (PR #1099 P2.1)
    and emits `pg_notify('operator_intent', …)`; schedd
    (the ONLY writer to `instances` per CLAUDE.md §6.2) is
    the sole consumer and dispatches via
    `engine.ParkWithReason` so the `pkg/state/machine.go`
    `CanTransition` guard fires. The handler returns 202
    Accepted with an intent_id; the operator polls
    GET /v1/admin/operator-intents/{id} for terminal state.

    `?confirm=true` is required as a tripwire against
    operator fat-fingering. Optional `?reason=<slug>` defaults
    to `operator_force_park`; values are clamped to the
    `[a-z0-9_]{1,64}` shape.

    ADR-1524: requires an MFA-verified operator session with step-up in
    the last five minutes, admin scope, the operator allowlist and same
    origin. Bearer keys do not satisfy step-up. Requires one
    Idempotency-Key (1-255 visible ASCII characters), an empty body,
    exactly one confirm=true and at most one reason; other query
    parameters are rejected. The key is scoped to the actor across all
    three recovery routes. Intent, exact 202 response and attributed
    request audit handoff commit atomically. Matching retries return
    the original response before reading mutable target state, without
    expiry; changed action, target or reason returns 409
    operator_submission_conflict. Trace changes do not change identity.
    An existing legacy recovery cache/reservation returns 409
    operator_submission_legacy and requires reconciliation. Notification
    after acceptance is best effort; the scheduler safety tick recovers
    a lost notification.

    Args:
        id (UUID):
        confirm (PostForceParkInstanceConfirm):
        reason (str | Unset):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorIntentAcceptedResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        confirm=confirm,
        reason=reason,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: PostForceParkInstanceConfirm,
    reason: str | Unset = UNSET,
    idempotency_key: str,
) -> Response[OperatorIntentAcceptedResponse | Problem]:
    """Enqueue a force-park intent for a wedged live instance (admin-only).

     Operator-side recovery primitive for instances wedged in
    {RUNNING, WAKING, COLD_BOOTING} that the customer can't
    wait for the idle reaper to handle. PR #1099 P2 redesign:
    apid writes a row to `operator_intents` (PR #1099 P2.1)
    and emits `pg_notify('operator_intent', …)`; schedd
    (the ONLY writer to `instances` per CLAUDE.md §6.2) is
    the sole consumer and dispatches via
    `engine.ParkWithReason` so the `pkg/state/machine.go`
    `CanTransition` guard fires. The handler returns 202
    Accepted with an intent_id; the operator polls
    GET /v1/admin/operator-intents/{id} for terminal state.

    `?confirm=true` is required as a tripwire against
    operator fat-fingering. Optional `?reason=<slug>` defaults
    to `operator_force_park`; values are clamped to the
    `[a-z0-9_]{1,64}` shape.

    ADR-1524: requires an MFA-verified operator session with step-up in
    the last five minutes, admin scope, the operator allowlist and same
    origin. Bearer keys do not satisfy step-up. Requires one
    Idempotency-Key (1-255 visible ASCII characters), an empty body,
    exactly one confirm=true and at most one reason; other query
    parameters are rejected. The key is scoped to the actor across all
    three recovery routes. Intent, exact 202 response and attributed
    request audit handoff commit atomically. Matching retries return
    the original response before reading mutable target state, without
    expiry; changed action, target or reason returns 409
    operator_submission_conflict. Trace changes do not change identity.
    An existing legacy recovery cache/reservation returns 409
    operator_submission_legacy and requires reconciliation. Notification
    after acceptance is best effort; the scheduler safety tick recovers
    a lost notification.

    Args:
        id (UUID):
        confirm (PostForceParkInstanceConfirm):
        reason (str | Unset):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorIntentAcceptedResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        confirm=confirm,
        reason=reason,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: PostForceParkInstanceConfirm,
    reason: str | Unset = UNSET,
    idempotency_key: str,
) -> OperatorIntentAcceptedResponse | Problem | None:
    """Enqueue a force-park intent for a wedged live instance (admin-only).

     Operator-side recovery primitive for instances wedged in
    {RUNNING, WAKING, COLD_BOOTING} that the customer can't
    wait for the idle reaper to handle. PR #1099 P2 redesign:
    apid writes a row to `operator_intents` (PR #1099 P2.1)
    and emits `pg_notify('operator_intent', …)`; schedd
    (the ONLY writer to `instances` per CLAUDE.md §6.2) is
    the sole consumer and dispatches via
    `engine.ParkWithReason` so the `pkg/state/machine.go`
    `CanTransition` guard fires. The handler returns 202
    Accepted with an intent_id; the operator polls
    GET /v1/admin/operator-intents/{id} for terminal state.

    `?confirm=true` is required as a tripwire against
    operator fat-fingering. Optional `?reason=<slug>` defaults
    to `operator_force_park`; values are clamped to the
    `[a-z0-9_]{1,64}` shape.

    ADR-1524: requires an MFA-verified operator session with step-up in
    the last five minutes, admin scope, the operator allowlist and same
    origin. Bearer keys do not satisfy step-up. Requires one
    Idempotency-Key (1-255 visible ASCII characters), an empty body,
    exactly one confirm=true and at most one reason; other query
    parameters are rejected. The key is scoped to the actor across all
    three recovery routes. Intent, exact 202 response and attributed
    request audit handoff commit atomically. Matching retries return
    the original response before reading mutable target state, without
    expiry; changed action, target or reason returns 409
    operator_submission_conflict. Trace changes do not change identity.
    An existing legacy recovery cache/reservation returns 409
    operator_submission_legacy and requires reconciliation. Notification
    after acceptance is best effort; the scheduler safety tick recovers
    a lost notification.

    Args:
        id (UUID):
        confirm (PostForceParkInstanceConfirm):
        reason (str | Unset):
        idempotency_key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorIntentAcceptedResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            confirm=confirm,
            reason=reason,
            idempotency_key=idempotency_key,
        )
    ).parsed
