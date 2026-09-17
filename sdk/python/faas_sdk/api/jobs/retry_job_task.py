from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.job_task_retry_response import JobTaskRetryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    name: str,
    id: UUID,
    idx: int,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/jobs/{name}/runs/{id}/tasks/{idx}/retry".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
            idx=quote(str(idx), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> JobTaskRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = JobTaskRetryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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
) -> Response[JobTaskRetryResponse | Problem]:
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
    idempotency_key: str | Unset = UNSET,
) -> Response[JobTaskRetryResponse | Problem]:
    """Retry one failed job task.

     Re-queues a failed, timeout, OOM, or cancelled task while its
    configured retry budget remains. The server applies the same capped
    exponential backoff as automatic retries and reopens a dead-letter run.

    Args:
        name (str):
        id (UUID):
        idx (int):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobTaskRetryResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        idempotency_key=idempotency_key,
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
    idempotency_key: str | Unset = UNSET,
) -> JobTaskRetryResponse | Problem | None:
    """Retry one failed job task.

     Re-queues a failed, timeout, OOM, or cancelled task while its
    configured retry budget remains. The server applies the same capped
    exponential backoff as automatic retries and reopens a dead-letter run.

    Args:
        name (str):
        id (UUID):
        idx (int):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobTaskRetryResponse | Problem
    """

    return sync_detailed(
        name=name,
        id=id,
        idx=idx,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[JobTaskRetryResponse | Problem]:
    """Retry one failed job task.

     Re-queues a failed, timeout, OOM, or cancelled task while its
    configured retry budget remains. The server applies the same capped
    exponential backoff as automatic retries and reopens a dead-letter run.

    Args:
        name (str):
        id (UUID):
        idx (int):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobTaskRetryResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> JobTaskRetryResponse | Problem | None:
    """Retry one failed job task.

     Re-queues a failed, timeout, OOM, or cancelled task while its
    configured retry budget remains. The server applies the same capped
    exponential backoff as automatic retries and reopens a dead-letter run.

    Args:
        name (str):
        id (UUID):
        idx (int):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobTaskRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            idx=idx,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
