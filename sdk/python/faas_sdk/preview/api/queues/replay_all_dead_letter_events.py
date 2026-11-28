from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dead_letter_replay_all_response import DeadLetterReplayAllResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    limit: int | Unset = 20,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/dlq:replay_all".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeadLetterReplayAllResponse | Problem | None:
    if response.status_code == 202:
        response_202 = DeadLetterReplayAllResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[DeadLetterReplayAllResponse | Problem]:
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
    limit: int | Unset = 20,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeadLetterReplayAllResponse | Problem]:
    """Replay pending dead-letter events for an app.

     Atomically resets up to `limit` pending queue invocation, broker
    trigger, and outbound webhook delivery records to pending and stamps
    each ledger row with replayed_at. Concurrent operators claim disjoint
    rows.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeadLetterReplayAllResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
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
    limit: int | Unset = 20,
    idempotency_key: str | Unset = UNSET,
) -> DeadLetterReplayAllResponse | Problem | None:
    """Replay pending dead-letter events for an app.

     Atomically resets up to `limit` pending queue invocation, broker
    trigger, and outbound webhook delivery records to pending and stamps
    each ledger row with replayed_at. Concurrent operators claim disjoint
    rows.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeadLetterReplayAllResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        limit=limit,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeadLetterReplayAllResponse | Problem]:
    """Replay pending dead-letter events for an app.

     Atomically resets up to `limit` pending queue invocation, broker
    trigger, and outbound webhook delivery records to pending and stamps
    each ledger row with replayed_at. Concurrent operators claim disjoint
    rows.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeadLetterReplayAllResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    idempotency_key: str | Unset = UNSET,
) -> DeadLetterReplayAllResponse | Problem | None:
    """Replay pending dead-letter events for an app.

     Atomically resets up to `limit` pending queue invocation, broker
    trigger, and outbound webhook delivery records to pending and stamps
    each ledger row with replayed_at. Concurrent operators claim disjoint
    rows.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeadLetterReplayAllResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            limit=limit,
            idempotency_key=idempotency_key,
        )
    ).parsed
