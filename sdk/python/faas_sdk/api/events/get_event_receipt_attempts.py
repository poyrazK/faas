from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_receipt_attempt_history_response import EventReceiptAttemptHistoryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    source: str,
    id: str,
    subscription_id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["source"] = source

    params["id"] = id

    params["subscription_id"] = subscription_id

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/events/receipt/attempts",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReceiptAttemptHistoryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventReceiptAttemptHistoryResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventReceiptAttemptHistoryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[EventReceiptAttemptHistoryResponse | Problem]:
    """Inspect retained handler delivery attempts for one captured recipient.

     Requires apps:read or admin. Includes the original async invocation and
    trusted child replays, ordered by descending attempt history ID. Each
    claim records its attempt and replay generation atomically with dispatch.
    A claim does not prove handler execution. Recovered expired leases have
    outcome unknown; external side effects still require idempotency.
    Only attempts recorded after rollout and still retained are available.
    Closed attempts expire after at most 30 days, earlier when result retention
    expires or their invocation is deleted. Running attempts are not pruned.
    Missing history does not establish that no delivery occurred. Unknown or
    foreign receipts, captured recipients and current app owners return 404.
    Cursors bind account, source, event ID, recipient and retained receipt;
    stale or mismatched cursors return 400. New claims do not reorder older
    pages; a running outcome may settle between reads.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReceiptAttemptHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
        subscription_id=subscription_id,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> EventReceiptAttemptHistoryResponse | Problem | None:
    """Inspect retained handler delivery attempts for one captured recipient.

     Requires apps:read or admin. Includes the original async invocation and
    trusted child replays, ordered by descending attempt history ID. Each
    claim records its attempt and replay generation atomically with dispatch.
    A claim does not prove handler execution. Recovered expired leases have
    outcome unknown; external side effects still require idempotency.
    Only attempts recorded after rollout and still retained are available.
    Closed attempts expire after at most 30 days, earlier when result retention
    expires or their invocation is deleted. Running attempts are not pruned.
    Missing history does not establish that no delivery occurred. Unknown or
    foreign receipts, captured recipients and current app owners return 404.
    Cursors bind account, source, event ID, recipient and retained receipt;
    stale or mismatched cursors return 400. New claims do not reorder older
    pages; a running outcome may settle between reads.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReceiptAttemptHistoryResponse | Problem
    """

    return sync_detailed(
        client=client,
        source=source,
        id=id,
        subscription_id=subscription_id,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[EventReceiptAttemptHistoryResponse | Problem]:
    """Inspect retained handler delivery attempts for one captured recipient.

     Requires apps:read or admin. Includes the original async invocation and
    trusted child replays, ordered by descending attempt history ID. Each
    claim records its attempt and replay generation atomically with dispatch.
    A claim does not prove handler execution. Recovered expired leases have
    outcome unknown; external side effects still require idempotency.
    Only attempts recorded after rollout and still retained are available.
    Closed attempts expire after at most 30 days, earlier when result retention
    expires or their invocation is deleted. Running attempts are not pruned.
    Missing history does not establish that no delivery occurred. Unknown or
    foreign receipts, captured recipients and current app owners return 404.
    Cursors bind account, source, event ID, recipient and retained receipt;
    stale or mismatched cursors return 400. New claims do not reorder older
    pages; a running outcome may settle between reads.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReceiptAttemptHistoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
        subscription_id=subscription_id,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> EventReceiptAttemptHistoryResponse | Problem | None:
    """Inspect retained handler delivery attempts for one captured recipient.

     Requires apps:read or admin. Includes the original async invocation and
    trusted child replays, ordered by descending attempt history ID. Each
    claim records its attempt and replay generation atomically with dispatch.
    A claim does not prove handler execution. Recovered expired leases have
    outcome unknown; external side effects still require idempotency.
    Only attempts recorded after rollout and still retained are available.
    Closed attempts expire after at most 30 days, earlier when result retention
    expires or their invocation is deleted. Running attempts are not pruned.
    Missing history does not establish that no delivery occurred. Unknown or
    foreign receipts, captured recipients and current app owners return 404.
    Cursors bind account, source, event ID, recipient and retained receipt;
    stale or mismatched cursors return 400. New claims do not reorder older
    pages; a running outcome may settle between reads.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReceiptAttemptHistoryResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            source=source,
            id=id,
            subscription_id=subscription_id,
            after=after,
            limit=limit,
        )
    ).parsed
