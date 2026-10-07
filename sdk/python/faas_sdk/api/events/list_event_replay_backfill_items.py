from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_replay_backfill_items_response import EventReplayBackfillItemsResponse
from ...models.list_event_replay_backfill_items_state import (
    ListEventReplayBackfillItemsState,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    job_id: UUID,
    *,
    state: ListEventReplayBackfillItemsState | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/event-replays/{job_id}/items".format(
            job_id=quote(str(job_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReplayBackfillItemsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventReplayBackfillItemsResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventReplayBackfillItemsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    state: ListEventReplayBackfillItemsState | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventReplayBackfillItemsResponse | Problem]:
    """List per-envelope outcomes for a durable backfill.

     Returns stable, acceptance-ordered metadata pages. Event identity and routing outcomes remain
    readable after the source envelope is pruned; event payloads are never returned.

    Args:
        job_id (UUID):
        state (ListEventReplayBackfillItemsState | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayBackfillItemsResponse | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        state=state,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    state: ListEventReplayBackfillItemsState | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventReplayBackfillItemsResponse | Problem | None:
    """List per-envelope outcomes for a durable backfill.

     Returns stable, acceptance-ordered metadata pages. Event identity and routing outcomes remain
    readable after the source envelope is pruned; event payloads are never returned.

    Args:
        job_id (UUID):
        state (ListEventReplayBackfillItemsState | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayBackfillItemsResponse | Problem
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        state=state,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    state: ListEventReplayBackfillItemsState | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventReplayBackfillItemsResponse | Problem]:
    """List per-envelope outcomes for a durable backfill.

     Returns stable, acceptance-ordered metadata pages. Event identity and routing outcomes remain
    readable after the source envelope is pruned; event payloads are never returned.

    Args:
        job_id (UUID):
        state (ListEventReplayBackfillItemsState | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayBackfillItemsResponse | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        state=state,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    state: ListEventReplayBackfillItemsState | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventReplayBackfillItemsResponse | Problem | None:
    """List per-envelope outcomes for a durable backfill.

     Returns stable, acceptance-ordered metadata pages. Event identity and routing outcomes remain
    readable after the source envelope is pruned; event payloads are never returned.

    Args:
        job_id (UUID):
        state (ListEventReplayBackfillItemsState | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayBackfillItemsResponse | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            state=state,
            after=after,
            limit=limit,
        )
    ).parsed
