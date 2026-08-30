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
    slug: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/queues/dead_letter/{id}/replay".format(
            slug=quote(str(slug), safe=""),
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
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AsyncInvokeResponse | Problem]:
    """Reset a dead-letter queue row back to pending.

     ADR-134 PR-C. Resets the row's `state` to `pending` with
    `attempts=0`, `last_error=null`, `due_at=now()`,
    `last_replayed_at=now()`. Distinct from
    `POST /v1/invocations/{id}/replay`, which enqueues a NEW row
    tagged Source=InvocationReplay. This endpoint mutates the
    existing row in place so the dashboard's replay history view
    tracks the chain on a single row id.

    Idempotent: a second POST after the first has succeeded
    finds the row in 'pending' and returns 404. The
    Idempotency-Key middleware (issued automatically by the SDK)
    covers double-POST across network retries.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AsyncInvokeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> AsyncInvokeResponse | Problem | None:
    """Reset a dead-letter queue row back to pending.

     ADR-134 PR-C. Resets the row's `state` to `pending` with
    `attempts=0`, `last_error=null`, `due_at=now()`,
    `last_replayed_at=now()`. Distinct from
    `POST /v1/invocations/{id}/replay`, which enqueues a NEW row
    tagged Source=InvocationReplay. This endpoint mutates the
    existing row in place so the dashboard's replay history view
    tracks the chain on a single row id.

    Idempotent: a second POST after the first has succeeded
    finds the row in 'pending' and returns 404. The
    Idempotency-Key middleware (issued automatically by the SDK)
    covers double-POST across network retries.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AsyncInvokeResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AsyncInvokeResponse | Problem]:
    """Reset a dead-letter queue row back to pending.

     ADR-134 PR-C. Resets the row's `state` to `pending` with
    `attempts=0`, `last_error=null`, `due_at=now()`,
    `last_replayed_at=now()`. Distinct from
    `POST /v1/invocations/{id}/replay`, which enqueues a NEW row
    tagged Source=InvocationReplay. This endpoint mutates the
    existing row in place so the dashboard's replay history view
    tracks the chain on a single row id.

    Idempotent: a second POST after the first has succeeded
    finds the row in 'pending' and returns 404. The
    Idempotency-Key middleware (issued automatically by the SDK)
    covers double-POST across network retries.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AsyncInvokeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> AsyncInvokeResponse | Problem | None:
    """Reset a dead-letter queue row back to pending.

     ADR-134 PR-C. Resets the row's `state` to `pending` with
    `attempts=0`, `last_error=null`, `due_at=now()`,
    `last_replayed_at=now()`. Distinct from
    `POST /v1/invocations/{id}/replay`, which enqueues a NEW row
    tagged Source=InvocationReplay. This endpoint mutates the
    existing row in place so the dashboard's replay history view
    tracks the chain on a single row id.

    Idempotent: a second POST after the first has succeeded
    finds the row in 'pending' and returns 404. The
    Idempotency-Key middleware (issued automatically by the SDK)
    covers double-POST across network retries.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AsyncInvokeResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
