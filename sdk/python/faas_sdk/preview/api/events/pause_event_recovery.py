from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_control_request import EventRecoveryControlRequest
from ...models.event_recovery_job import EventRecoveryJob
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    job_id: UUID,
    *,
    body: EventRecoveryControlRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/event-recoveries/{job_id}/pause".format(
            job_id=quote(str(job_id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryJob | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryJob.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRecoveryJob | Problem]:
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
    body: EventRecoveryControlRequest | Unset = UNSET,
) -> Response[EventRecoveryJob | Problem]:
    """Pause further recovery admissions.

     Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window
    budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed,
    cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are
    idempotent.

    Args:
        job_id (UUID):
        body (EventRecoveryControlRequest | Unset): Optional audit reason attached to a recovery
            pause, resume or cancellation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryJob | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventRecoveryControlRequest | Unset = UNSET,
) -> EventRecoveryJob | Problem | None:
    """Pause further recovery admissions.

     Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window
    budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed,
    cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are
    idempotent.

    Args:
        job_id (UUID):
        body (EventRecoveryControlRequest | Unset): Optional audit reason attached to a recovery
            pause, resume or cancellation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryJob | Problem
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventRecoveryControlRequest | Unset = UNSET,
) -> Response[EventRecoveryJob | Problem]:
    """Pause further recovery admissions.

     Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window
    budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed,
    cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are
    idempotent.

    Args:
        job_id (UUID):
        body (EventRecoveryControlRequest | Unset): Optional audit reason attached to a recovery
            pause, resume or cancellation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryJob | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventRecoveryControlRequest | Unset = UNSET,
) -> EventRecoveryJob | Problem | None:
    """Pause further recovery admissions.

     Requires `deploy:write` or `admin` and MFA. Controls preserve the frozen selection, spent window
    budget, existing waits, quota, and original expiry. Already queued deliveries continue. Completed,
    cancelled, or expired jobs return 409. Repeated desired-state controls on active jobs are
    idempotent.

    Args:
        job_id (UUID):
        body (EventRecoveryControlRequest | Unset): Optional audit reason attached to a recovery
            pause, resume or cancellation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryJob | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            body=body,
        )
    ).parsed
