from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_job_request import CreateJobRequest
from ...models.job_response import JobResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateJobRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/jobs",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> JobResponse | Problem | None:
    if response.status_code == 201:
        response_201 = JobResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[JobResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateJobRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[JobResponse | Problem]:
    """Create a job template.

     Plan-tier gate (Free → 402 jobs_not_allowed) precedes
    per-plan cap clamping (RAM, task_timeout, parallelism,
    retry_max). The per-account JobMaxPerAccount quota is
    enforced atomically (PR-A → PR-B style follow-up).

    Args:
        idempotency_key (str | Unset):
        body (CreateJobRequest): Job creation payload — name + image + command + caps; schedule
            enables recurring runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateJobRequest,
    idempotency_key: str | Unset = UNSET,
) -> JobResponse | Problem | None:
    """Create a job template.

     Plan-tier gate (Free → 402 jobs_not_allowed) precedes
    per-plan cap clamping (RAM, task_timeout, parallelism,
    retry_max). The per-account JobMaxPerAccount quota is
    enforced atomically (PR-A → PR-B style follow-up).

    Args:
        idempotency_key (str | Unset):
        body (CreateJobRequest): Job creation payload — name + image + command + caps; schedule
            enables recurring runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateJobRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[JobResponse | Problem]:
    """Create a job template.

     Plan-tier gate (Free → 402 jobs_not_allowed) precedes
    per-plan cap clamping (RAM, task_timeout, parallelism,
    retry_max). The per-account JobMaxPerAccount quota is
    enforced atomically (PR-A → PR-B style follow-up).

    Args:
        idempotency_key (str | Unset):
        body (CreateJobRequest): Job creation payload — name + image + command + caps; schedule
            enables recurring runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateJobRequest,
    idempotency_key: str | Unset = UNSET,
) -> JobResponse | Problem | None:
    """Create a job template.

     Plan-tier gate (Free → 402 jobs_not_allowed) precedes
    per-plan cap clamping (RAM, task_timeout, parallelism,
    retry_max). The per-account JobMaxPerAccount quota is
    enforced atomically (PR-A → PR-B style follow-up).

    Args:
        idempotency_key (str | Unset):
        body (CreateJobRequest): Job creation payload — name + image + command + caps; schedule
            enables recurring runs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
