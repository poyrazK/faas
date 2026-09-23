from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.recover_rollout_request import RecoverRolloutRequest
from ...models.rollout_transition_response import RolloutTransitionResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: RecoverRolloutRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/rollouts/recover".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RolloutTransitionResponse | None:
    if response.status_code == 200:
        response_200 = RolloutTransitionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = RolloutTransitionResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RolloutTransitionResponse]:
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
    body: RecoverRolloutRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | RolloutTransitionResponse]:
    r"""Operator manual rollout recovery (SAFE-RELEASES-R, issue

     The operator escape hatch for a stuck canary rollout. Three
    closed-set actions:

      - `advance`: bumps `canary_step` by 1, stamps
        `canary_step_started_at = now()`, and redistributes the
        traffic-split (largest-remainder Σ = 100). Requires the
        rollout to be stuck (`canary_step_started_at` older than
        the canned 30-minute stuck-after window). On a healthy
        rollout the handler returns 409 `rollout_not_stuck`
        with the suggestion \"use --action promote instead\".

      - `promote`: short-circuits the rollout to
        `canary_step = canary_total_steps` and
        `rollout_state = 'complete'`, with `traffic_percent = 100`
        on the in-flight row + 0 on the siblings. No stuck-check;
        this is the operator's \"I'm sure, ship it\" path.

      - `abort`: ordinary canaries transition synchronously to
        `rollout_state = 'aborted'`. A zero-step service rollout instead
        records a durable abort request and returns 202; schedd restores
        the predecessor route, waits for gateway acknowledgement and
        candidate request drain, then finalises the abort. `advance` and
        `promote` are invalid for service rollouts. Emits a
        `deploy.rolled_back` audit row for the operator intent.

    Returns the post-transition Deployment + the audit row id
    so the operator's terminal can echo `audit_id=…`. Plan-tier
    gated to Pro+ (Hobby / Free get 403
    `plan_traffic_split_not_allowed`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RecoverRolloutRequest): Body for POST /v1/apps/{slug}/rollouts/recover (SAFE-
            RELEASES-R, issue #976 / ADR-122). Closed-set `action` ∈ {advance, promote, abort};
            `reason` is the operator-supplied free-text captured into the deployment_audit row's data
            payload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RolloutTransitionResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecoverRolloutRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | RolloutTransitionResponse | None:
    r"""Operator manual rollout recovery (SAFE-RELEASES-R, issue

     The operator escape hatch for a stuck canary rollout. Three
    closed-set actions:

      - `advance`: bumps `canary_step` by 1, stamps
        `canary_step_started_at = now()`, and redistributes the
        traffic-split (largest-remainder Σ = 100). Requires the
        rollout to be stuck (`canary_step_started_at` older than
        the canned 30-minute stuck-after window). On a healthy
        rollout the handler returns 409 `rollout_not_stuck`
        with the suggestion \"use --action promote instead\".

      - `promote`: short-circuits the rollout to
        `canary_step = canary_total_steps` and
        `rollout_state = 'complete'`, with `traffic_percent = 100`
        on the in-flight row + 0 on the siblings. No stuck-check;
        this is the operator's \"I'm sure, ship it\" path.

      - `abort`: ordinary canaries transition synchronously to
        `rollout_state = 'aborted'`. A zero-step service rollout instead
        records a durable abort request and returns 202; schedd restores
        the predecessor route, waits for gateway acknowledgement and
        candidate request drain, then finalises the abort. `advance` and
        `promote` are invalid for service rollouts. Emits a
        `deploy.rolled_back` audit row for the operator intent.

    Returns the post-transition Deployment + the audit row id
    so the operator's terminal can echo `audit_id=…`. Plan-tier
    gated to Pro+ (Hobby / Free get 403
    `plan_traffic_split_not_allowed`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RecoverRolloutRequest): Body for POST /v1/apps/{slug}/rollouts/recover (SAFE-
            RELEASES-R, issue #976 / ADR-122). Closed-set `action` ∈ {advance, promote, abort};
            `reason` is the operator-supplied free-text captured into the deployment_audit row's data
            payload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RolloutTransitionResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecoverRolloutRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | RolloutTransitionResponse]:
    r"""Operator manual rollout recovery (SAFE-RELEASES-R, issue

     The operator escape hatch for a stuck canary rollout. Three
    closed-set actions:

      - `advance`: bumps `canary_step` by 1, stamps
        `canary_step_started_at = now()`, and redistributes the
        traffic-split (largest-remainder Σ = 100). Requires the
        rollout to be stuck (`canary_step_started_at` older than
        the canned 30-minute stuck-after window). On a healthy
        rollout the handler returns 409 `rollout_not_stuck`
        with the suggestion \"use --action promote instead\".

      - `promote`: short-circuits the rollout to
        `canary_step = canary_total_steps` and
        `rollout_state = 'complete'`, with `traffic_percent = 100`
        on the in-flight row + 0 on the siblings. No stuck-check;
        this is the operator's \"I'm sure, ship it\" path.

      - `abort`: ordinary canaries transition synchronously to
        `rollout_state = 'aborted'`. A zero-step service rollout instead
        records a durable abort request and returns 202; schedd restores
        the predecessor route, waits for gateway acknowledgement and
        candidate request drain, then finalises the abort. `advance` and
        `promote` are invalid for service rollouts. Emits a
        `deploy.rolled_back` audit row for the operator intent.

    Returns the post-transition Deployment + the audit row id
    so the operator's terminal can echo `audit_id=…`. Plan-tier
    gated to Pro+ (Hobby / Free get 403
    `plan_traffic_split_not_allowed`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RecoverRolloutRequest): Body for POST /v1/apps/{slug}/rollouts/recover (SAFE-
            RELEASES-R, issue #976 / ADR-122). Closed-set `action` ∈ {advance, promote, abort};
            `reason` is the operator-supplied free-text captured into the deployment_audit row's data
            payload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RolloutTransitionResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecoverRolloutRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | RolloutTransitionResponse | None:
    r"""Operator manual rollout recovery (SAFE-RELEASES-R, issue

     The operator escape hatch for a stuck canary rollout. Three
    closed-set actions:

      - `advance`: bumps `canary_step` by 1, stamps
        `canary_step_started_at = now()`, and redistributes the
        traffic-split (largest-remainder Σ = 100). Requires the
        rollout to be stuck (`canary_step_started_at` older than
        the canned 30-minute stuck-after window). On a healthy
        rollout the handler returns 409 `rollout_not_stuck`
        with the suggestion \"use --action promote instead\".

      - `promote`: short-circuits the rollout to
        `canary_step = canary_total_steps` and
        `rollout_state = 'complete'`, with `traffic_percent = 100`
        on the in-flight row + 0 on the siblings. No stuck-check;
        this is the operator's \"I'm sure, ship it\" path.

      - `abort`: ordinary canaries transition synchronously to
        `rollout_state = 'aborted'`. A zero-step service rollout instead
        records a durable abort request and returns 202; schedd restores
        the predecessor route, waits for gateway acknowledgement and
        candidate request drain, then finalises the abort. `advance` and
        `promote` are invalid for service rollouts. Emits a
        `deploy.rolled_back` audit row for the operator intent.

    Returns the post-transition Deployment + the audit row id
    so the operator's terminal can echo `audit_id=…`. Plan-tier
    gated to Pro+ (Hobby / Free get 403
    `plan_traffic_split_not_allowed`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RecoverRolloutRequest): Body for POST /v1/apps/{slug}/rollouts/recover (SAFE-
            RELEASES-R, issue #976 / ADR-122). Closed-set `action` ∈ {advance, promote, abort};
            `reason` is the operator-supplied free-text captured into the deployment_audit row's data
            payload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RolloutTransitionResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
