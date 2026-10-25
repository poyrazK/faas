from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    name: str,
    id: UUID,
    idx: int,
    *,
    since_seq: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since_seq"] = since_seq

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
            idx=quote(str(idx), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 200:
        response_200 = cast(Any, None)
        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    since_seq: int | Unset = UNSET,
) -> Response[Any | Problem]:
    """Stream a task's output live (SSE).

     ADR-1163. Served by the compute gateway, not apid. Waits while the
    task is queued, then relays the task VM's console output as it is
    written, including each retry attempt in turn. Frames match the app
    log stream: `event: log` (seq, instance, stream, line, written_at),
    `event: gap` when the VM already dropped older lines, heartbeat
    comments, and one final `event: end` whose `reason` is
    `task_finished` (all output relayed), `stored_output` (the task ended
    before its last VM's output could be streamed; read `GET .../logs`),
    `timeout`, `node_unreachable` or `not_found`. Read the task for its
    outcome; the stored output from `GET .../logs` remains available.

    Args:
        name (str):
        id (UUID):
        idx (int):
        since_seq (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        since_seq=since_seq,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    since_seq: int | Unset = UNSET,
) -> Any | Problem | None:
    """Stream a task's output live (SSE).

     ADR-1163. Served by the compute gateway, not apid. Waits while the
    task is queued, then relays the task VM's console output as it is
    written, including each retry attempt in turn. Frames match the app
    log stream: `event: log` (seq, instance, stream, line, written_at),
    `event: gap` when the VM already dropped older lines, heartbeat
    comments, and one final `event: end` whose `reason` is
    `task_finished` (all output relayed), `stored_output` (the task ended
    before its last VM's output could be streamed; read `GET .../logs`),
    `timeout`, `node_unreachable` or `not_found`. Read the task for its
    outcome; the stored output from `GET .../logs` remains available.

    Args:
        name (str):
        id (UUID):
        idx (int):
        since_seq (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        name=name,
        id=id,
        idx=idx,
        client=client,
        since_seq=since_seq,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    since_seq: int | Unset = UNSET,
) -> Response[Any | Problem]:
    """Stream a task's output live (SSE).

     ADR-1163. Served by the compute gateway, not apid. Waits while the
    task is queued, then relays the task VM's console output as it is
    written, including each retry attempt in turn. Frames match the app
    log stream: `event: log` (seq, instance, stream, line, written_at),
    `event: gap` when the VM already dropped older lines, heartbeat
    comments, and one final `event: end` whose `reason` is
    `task_finished` (all output relayed), `stored_output` (the task ended
    before its last VM's output could be streamed; read `GET .../logs`),
    `timeout`, `node_unreachable` or `not_found`. Read the task for its
    outcome; the stored output from `GET .../logs` remains available.

    Args:
        name (str):
        id (UUID):
        idx (int):
        since_seq (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        since_seq=since_seq,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    since_seq: int | Unset = UNSET,
) -> Any | Problem | None:
    """Stream a task's output live (SSE).

     ADR-1163. Served by the compute gateway, not apid. Waits while the
    task is queued, then relays the task VM's console output as it is
    written, including each retry attempt in turn. Frames match the app
    log stream: `event: log` (seq, instance, stream, line, written_at),
    `event: gap` when the VM already dropped older lines, heartbeat
    comments, and one final `event: end` whose `reason` is
    `task_finished` (all output relayed), `stored_output` (the task ended
    before its last VM's output could be streamed; read `GET .../logs`),
    `timeout`, `node_unreachable` or `not_found`. Read the task for its
    outcome; the stored output from `GET .../logs` remains available.

    Args:
        name (str):
        id (UUID):
        idx (int):
        since_seq (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            idx=idx,
            client=client,
            since_seq=since_seq,
        )
    ).parsed
