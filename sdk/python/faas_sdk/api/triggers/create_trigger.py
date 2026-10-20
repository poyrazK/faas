from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_trigger_request import CreateTriggerRequest
from ...models.problem import Problem
from ...models.trigger import Trigger
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateTriggerRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/triggers",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | Trigger | None:
    if response.status_code == 201:
        response_201 = Trigger.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Problem | Trigger]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | Trigger]:
    """Create a trigger.

     Idempotent via Idempotency-Key header. Returns 402
    `plan_triggers_not_allowed` for Free plan. A 403 may be
    `trigger_kind_not_allowed`, `plan_trigger_quota`,
    `trigger_batch_window_too_large`, or
    `trigger_tls_skip_verify_not_allowed`; see ADR-100.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerRequest): Trigger create payload. Kind is immutable after create. Per-
            kind
            gating mirrors pkg/gregalemanifest.validateKindConfig:
              - cron: requires schedule + path (slug ignored)
              - non-cron: requires slug + config
            Omitted delivery settings use the platform default capped to the
            selected account plan; explicit over-cap values are rejected.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Trigger]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | Trigger | None:
    """Create a trigger.

     Idempotent via Idempotency-Key header. Returns 402
    `plan_triggers_not_allowed` for Free plan. A 403 may be
    `trigger_kind_not_allowed`, `plan_trigger_quota`,
    `trigger_batch_window_too_large`, or
    `trigger_tls_skip_verify_not_allowed`; see ADR-100.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerRequest): Trigger create payload. Kind is immutable after create. Per-
            kind
            gating mirrors pkg/gregalemanifest.validateKindConfig:
              - cron: requires schedule + path (slug ignored)
              - non-cron: requires slug + config
            Omitted delivery settings use the platform default capped to the
            selected account plan; explicit over-cap values are rejected.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Trigger
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | Trigger]:
    """Create a trigger.

     Idempotent via Idempotency-Key header. Returns 402
    `plan_triggers_not_allowed` for Free plan. A 403 may be
    `trigger_kind_not_allowed`, `plan_trigger_quota`,
    `trigger_batch_window_too_large`, or
    `trigger_tls_skip_verify_not_allowed`; see ADR-100.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerRequest): Trigger create payload. Kind is immutable after create. Per-
            kind
            gating mirrors pkg/gregalemanifest.validateKindConfig:
              - cron: requires schedule + path (slug ignored)
              - non-cron: requires slug + config
            Omitted delivery settings use the platform default capped to the
            selected account plan; explicit over-cap values are rejected.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Trigger]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | Trigger | None:
    """Create a trigger.

     Idempotent via Idempotency-Key header. Returns 402
    `plan_triggers_not_allowed` for Free plan. A 403 may be
    `trigger_kind_not_allowed`, `plan_trigger_quota`,
    `trigger_batch_window_too_large`, or
    `trigger_tls_skip_verify_not_allowed`; see ADR-100.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerRequest): Trigger create payload. Kind is immutable after create. Per-
            kind
            gating mirrors pkg/gregalemanifest.validateKindConfig:
              - cron: requires schedule + path (slug ignored)
              - non-cron: requires slug + config
            Omitted delivery settings use the platform default capped to the
            selected account plan; explicit over-cap values are rejected.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Trigger
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
