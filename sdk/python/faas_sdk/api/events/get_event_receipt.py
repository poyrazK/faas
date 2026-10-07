from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_receipt_response import EventReceiptResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    source: str,
    id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["source"] = source

    params["id"] = id

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/events/receipt",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReceiptResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventReceiptResponse.from_dict(response.json())

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
) -> Response[EventReceiptResponse | Problem]:
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
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[EventReceiptResponse | Problem]:
    """Inspect acceptance, routing, and execution for one event.

     Requires apps:read or admin. Reads the retained account/source/id receipt
    and a bounded page of immutable acceptance-time recipients. Routing
    counts cover the entire snapshot, including recipients without invocations.
    Enqueued routing does not imply handler success: keyed cancellation can
    produce a cancellation receipt instead. The original deterministic
    invocation is shown when retained; trusted generic replay lineage adds
    the latest replay and a paginated history URL without replacing the
    original failure. Recovery actions target the latest retained replay
    when present. Recovery actions require their existing
    write scopes and are revalidated when called. Legacy receipts without
    snapshots report snapshot_captured=false and cannot reconstruct recipients.
    Pages follow acceptance order while outcomes may change between requests.

    Args:
        source (str):
        id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
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
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> EventReceiptResponse | Problem | None:
    """Inspect acceptance, routing, and execution for one event.

     Requires apps:read or admin. Reads the retained account/source/id receipt
    and a bounded page of immutable acceptance-time recipients. Routing
    counts cover the entire snapshot, including recipients without invocations.
    Enqueued routing does not imply handler success: keyed cancellation can
    produce a cancellation receipt instead. The original deterministic
    invocation is shown when retained; trusted generic replay lineage adds
    the latest replay and a paginated history URL without replacing the
    original failure. Recovery actions target the latest retained replay
    when present. Recovery actions require their existing
    write scopes and are revalidated when called. Legacy receipts without
    snapshots report snapshot_captured=false and cannot reconstruct recipients.
    Pages follow acceptance order while outcomes may change between requests.

    Args:
        source (str):
        id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReceiptResponse | Problem
    """

    return sync_detailed(
        client=client,
        source=source,
        id=id,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[EventReceiptResponse | Problem]:
    """Inspect acceptance, routing, and execution for one event.

     Requires apps:read or admin. Reads the retained account/source/id receipt
    and a bounded page of immutable acceptance-time recipients. Routing
    counts cover the entire snapshot, including recipients without invocations.
    Enqueued routing does not imply handler success: keyed cancellation can
    produce a cancellation receipt instead. The original deterministic
    invocation is shown when retained; trusted generic replay lineage adds
    the latest replay and a paginated history URL without replacing the
    original failure. Recovery actions target the latest retained replay
    when present. Recovery actions require their existing
    write scopes and are revalidated when called. Legacy receipts without
    snapshots report snapshot_captured=false and cannot reconstruct recipients.
    Pages follow acceptance order while outcomes may change between requests.

    Args:
        source (str):
        id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
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
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> EventReceiptResponse | Problem | None:
    """Inspect acceptance, routing, and execution for one event.

     Requires apps:read or admin. Reads the retained account/source/id receipt
    and a bounded page of immutable acceptance-time recipients. Routing
    counts cover the entire snapshot, including recipients without invocations.
    Enqueued routing does not imply handler success: keyed cancellation can
    produce a cancellation receipt instead. The original deterministic
    invocation is shown when retained; trusted generic replay lineage adds
    the latest replay and a paginated history URL without replacing the
    original failure. Recovery actions target the latest retained replay
    when present. Recovery actions require their existing
    write scopes and are revalidated when called. Legacy receipts without
    snapshots report snapshot_captured=false and cannot reconstruct recipients.
    Pages follow acceptance order while outcomes may change between requests.

    Args:
        source (str):
        id (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReceiptResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            source=source,
            id=id,
            after=after,
            limit=limit,
        )
    ).parsed
