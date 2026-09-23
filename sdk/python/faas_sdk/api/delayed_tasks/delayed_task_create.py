from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.delayed_task_after_request import DelayedTaskAfterRequest
from ...models.delayed_task_at_request import DelayedTaskAtRequest
from ...models.delayed_task_response import DelayedTaskResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: DelayedTaskAfterRequest | DelayedTaskAtRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/delayed-tasks".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if isinstance(body, DelayedTaskAtRequest):
        _kwargs["json"] = body.to_dict()
    else:
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DelayedTaskResponse | Problem | None:
    if response.status_code == 201:
        response_201 = DelayedTaskResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DelayedTaskResponse | Problem]:
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
    body: DelayedTaskAfterRequest | DelayedTaskAtRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DelayedTaskResponse | Problem]:
    """Schedule a delayed task to fire at a future time.

     Supply exactly one of `scheduled_at` or `delay_seconds`. Scheduling is
    bounded to one year. Cap-checked against the plan's
    MaxDelayedTasksPerApp (Hobby 5, Pro 50, Scale 1_000_000). Accepted
    tasks are grandfathered across later plan changes. Delivery is at
    least once; handlers should use the invocation id to make side effects
    idempotent.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DelayedTaskAfterRequest | DelayedTaskAtRequest): Body for POST
            /v1/apps/{slug}/delayed-tasks. Supply exactly one scheduling form; the maximum delay is
            31,536,000 seconds (365 days).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DelayedTaskResponse | Problem]
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
    body: DelayedTaskAfterRequest | DelayedTaskAtRequest,
    idempotency_key: str | Unset = UNSET,
) -> DelayedTaskResponse | Problem | None:
    """Schedule a delayed task to fire at a future time.

     Supply exactly one of `scheduled_at` or `delay_seconds`. Scheduling is
    bounded to one year. Cap-checked against the plan's
    MaxDelayedTasksPerApp (Hobby 5, Pro 50, Scale 1_000_000). Accepted
    tasks are grandfathered across later plan changes. Delivery is at
    least once; handlers should use the invocation id to make side effects
    idempotent.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DelayedTaskAfterRequest | DelayedTaskAtRequest): Body for POST
            /v1/apps/{slug}/delayed-tasks. Supply exactly one scheduling form; the maximum delay is
            31,536,000 seconds (365 days).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DelayedTaskResponse | Problem
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
    body: DelayedTaskAfterRequest | DelayedTaskAtRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DelayedTaskResponse | Problem]:
    """Schedule a delayed task to fire at a future time.

     Supply exactly one of `scheduled_at` or `delay_seconds`. Scheduling is
    bounded to one year. Cap-checked against the plan's
    MaxDelayedTasksPerApp (Hobby 5, Pro 50, Scale 1_000_000). Accepted
    tasks are grandfathered across later plan changes. Delivery is at
    least once; handlers should use the invocation id to make side effects
    idempotent.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DelayedTaskAfterRequest | DelayedTaskAtRequest): Body for POST
            /v1/apps/{slug}/delayed-tasks. Supply exactly one scheduling form; the maximum delay is
            31,536,000 seconds (365 days).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DelayedTaskResponse | Problem]
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
    body: DelayedTaskAfterRequest | DelayedTaskAtRequest,
    idempotency_key: str | Unset = UNSET,
) -> DelayedTaskResponse | Problem | None:
    """Schedule a delayed task to fire at a future time.

     Supply exactly one of `scheduled_at` or `delay_seconds`. Scheduling is
    bounded to one year. Cap-checked against the plan's
    MaxDelayedTasksPerApp (Hobby 5, Pro 50, Scale 1_000_000). Accepted
    tasks are grandfathered across later plan changes. Delivery is at
    least once; handlers should use the invocation id to make side effects
    idempotent.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DelayedTaskAfterRequest | DelayedTaskAtRequest): Body for POST
            /v1/apps/{slug}/delayed-tasks. Supply exactly one scheduling form; the maximum delay is
            31,536,000 seconds (365 days).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DelayedTaskResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
