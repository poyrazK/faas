from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.async_invoke_response import AsyncInvokeResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/invocations/{id}/replay".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AsyncInvokeResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AsyncInvokeResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[AsyncInvokeResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AsyncInvokeResponse | Problem]:
    """Re-issue a failed or dead_letter invocation.

     Accepts no request body. Requires deployment write scope and configured
    MFA. Only failed or dead-lettered unbound unkeyed work is eligible.
    The parent and its current app must belong to the caller. The child
    preserves the original request, deployment scope, customer identity
    and trusted replay lineage. Trace/version headers and execution/result
    lifetimes are refreshed; retry policy uses the current app and plan.

    Each parent creates at most one durable recovery child. Concurrent and
    repeated requests return that child regardless of Idempotency-Key,
    including after completion. A subsequent recovery targets the failed
    child. If its child has been pruned, the retained parent returns 409
    `invocation_replay_unavailable` instead of creating another execution.
    Existing acceptance is returned before checking expired deployment pins.
    Application delivery and external side effects remain at least once.

    Other parent states return `invocation_not_replayable`. Keyed work
    returns `keyed_replay_requires_policy`; use `/replay-keyed` for failed
    keyed work. Queue-bound or named-queue work returns
    `queue_replay_requires_binding` and requires its app queue dead-letter
    replay endpoint.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AsyncInvokeResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> AsyncInvokeResponse | Problem | None:
    """Re-issue a failed or dead_letter invocation.

     Accepts no request body. Requires deployment write scope and configured
    MFA. Only failed or dead-lettered unbound unkeyed work is eligible.
    The parent and its current app must belong to the caller. The child
    preserves the original request, deployment scope, customer identity
    and trusted replay lineage. Trace/version headers and execution/result
    lifetimes are refreshed; retry policy uses the current app and plan.

    Each parent creates at most one durable recovery child. Concurrent and
    repeated requests return that child regardless of Idempotency-Key,
    including after completion. A subsequent recovery targets the failed
    child. If its child has been pruned, the retained parent returns 409
    `invocation_replay_unavailable` instead of creating another execution.
    Existing acceptance is returned before checking expired deployment pins.
    Application delivery and external side effects remain at least once.

    Other parent states return `invocation_not_replayable`. Keyed work
    returns `keyed_replay_requires_policy`; use `/replay-keyed` for failed
    keyed work. Queue-bound or named-queue work returns
    `queue_replay_requires_binding` and requires its app queue dead-letter
    replay endpoint.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AsyncInvokeResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AsyncInvokeResponse | Problem]:
    """Re-issue a failed or dead_letter invocation.

     Accepts no request body. Requires deployment write scope and configured
    MFA. Only failed or dead-lettered unbound unkeyed work is eligible.
    The parent and its current app must belong to the caller. The child
    preserves the original request, deployment scope, customer identity
    and trusted replay lineage. Trace/version headers and execution/result
    lifetimes are refreshed; retry policy uses the current app and plan.

    Each parent creates at most one durable recovery child. Concurrent and
    repeated requests return that child regardless of Idempotency-Key,
    including after completion. A subsequent recovery targets the failed
    child. If its child has been pruned, the retained parent returns 409
    `invocation_replay_unavailable` instead of creating another execution.
    Existing acceptance is returned before checking expired deployment pins.
    Application delivery and external side effects remain at least once.

    Other parent states return `invocation_not_replayable`. Keyed work
    returns `keyed_replay_requires_policy`; use `/replay-keyed` for failed
    keyed work. Queue-bound or named-queue work returns
    `queue_replay_requires_binding` and requires its app queue dead-letter
    replay endpoint.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AsyncInvokeResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> AsyncInvokeResponse | Problem | None:
    """Re-issue a failed or dead_letter invocation.

     Accepts no request body. Requires deployment write scope and configured
    MFA. Only failed or dead-lettered unbound unkeyed work is eligible.
    The parent and its current app must belong to the caller. The child
    preserves the original request, deployment scope, customer identity
    and trusted replay lineage. Trace/version headers and execution/result
    lifetimes are refreshed; retry policy uses the current app and plan.

    Each parent creates at most one durable recovery child. Concurrent and
    repeated requests return that child regardless of Idempotency-Key,
    including after completion. A subsequent recovery targets the failed
    child. If its child has been pruned, the retained parent returns 409
    `invocation_replay_unavailable` instead of creating another execution.
    Existing acceptance is returned before checking expired deployment pins.
    Application delivery and external side effects remain at least once.

    Other parent states return `invocation_not_replayable`. Keyed work
    returns `keyed_replay_requires_policy`; use `/replay-keyed` for failed
    keyed work. Queue-bound or named-queue work returns
    `queue_replay_requires_binding` and requires its app queue dead-letter
    replay endpoint.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AsyncInvokeResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
