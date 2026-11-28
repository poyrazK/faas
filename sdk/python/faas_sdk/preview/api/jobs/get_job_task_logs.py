from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.job_task_log_response import JobTaskLogResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    name: str,
    id: UUID,
    idx: int,
    *,
    max_bytes: int | Unset = 65536,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["max_bytes"] = max_bytes

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/jobs/{name}/runs/{id}/tasks/{idx}/logs".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
            idx=quote(str(idx), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> JobTaskLogResponse | Problem | None:
    if response.status_code == 200:
        response_200 = JobTaskLogResponse.from_dict(response.json())

        return response_200

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
) -> Response[JobTaskLogResponse | Problem]:
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
    max_bytes: int | Unset = 65536,
) -> Response[JobTaskLogResponse | Problem]:
    """Get tail logs of a task.

     Returns the durable combined stdout/stderr tail captured before
    the terminal task microVM is destroyed. Empty LogContent with
    Truncated=false means the task never produced output
    (process exited before writing anything — common for
    OOM-killed tasks).

    Args:
        name (str):
        id (UUID):
        idx (int):
        max_bytes (int | Unset):  Default: 65536.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobTaskLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        max_bytes=max_bytes,
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
    max_bytes: int | Unset = 65536,
) -> JobTaskLogResponse | Problem | None:
    """Get tail logs of a task.

     Returns the durable combined stdout/stderr tail captured before
    the terminal task microVM is destroyed. Empty LogContent with
    Truncated=false means the task never produced output
    (process exited before writing anything — common for
    OOM-killed tasks).

    Args:
        name (str):
        id (UUID):
        idx (int):
        max_bytes (int | Unset):  Default: 65536.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobTaskLogResponse | Problem
    """

    return sync_detailed(
        name=name,
        id=id,
        idx=idx,
        client=client,
        max_bytes=max_bytes,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    max_bytes: int | Unset = 65536,
) -> Response[JobTaskLogResponse | Problem]:
    """Get tail logs of a task.

     Returns the durable combined stdout/stderr tail captured before
    the terminal task microVM is destroyed. Empty LogContent with
    Truncated=false means the task never produced output
    (process exited before writing anything — common for
    OOM-killed tasks).

    Args:
        name (str):
        id (UUID):
        idx (int):
        max_bytes (int | Unset):  Default: 65536.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobTaskLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        max_bytes=max_bytes,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    max_bytes: int | Unset = 65536,
) -> JobTaskLogResponse | Problem | None:
    """Get tail logs of a task.

     Returns the durable combined stdout/stderr tail captured before
    the terminal task microVM is destroyed. Empty LogContent with
    Truncated=false means the task never produced output
    (process exited before writing anything — common for
    OOM-killed tasks).

    Args:
        name (str):
        id (UUID):
        idx (int):
        max_bytes (int | Unset):  Default: 65536.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobTaskLogResponse | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            idx=idx,
            client=client,
            max_bytes=max_bytes,
        )
    ).parsed
