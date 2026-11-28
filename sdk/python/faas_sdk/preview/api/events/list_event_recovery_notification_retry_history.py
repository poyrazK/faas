from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_notification_retry_history import EventRecoveryNotificationRetryHistory
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    job_id: UUID,
    *,
    status: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["status"] = status

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/event-recoveries/{job_id}/notification-retry-decisions".format(
            job_id=quote(str(job_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryNotificationRetryHistory | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryNotificationRetryHistory.from_dict(response.json())

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
) -> Response[EventRecoveryNotificationRetryHistory | Problem]:
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
    status: str | Unset = UNSET,
) -> Response[EventRecoveryNotificationRetryHistory | Problem]:
    """List saved recovery notification retry decisions.

     Requires apps:read or admin and MFA. Lists at most 100 immutable request summaries in decision time
    order for the retained owned recovery job. The optional status filter selects a comma-separated
    union of original-generation request statuses. Totals cover all retained requests before filtering;
    matched_count counts returned rows. Other query parameters, repeated status parameters, duplicate
    statuses, empty selections, and unknown statuses are rejected. Decisions expire when the job is
    pruned.

    Args:
        job_id (UUID):
        status (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryNotificationRetryHistory | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        status=status,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    status: str | Unset = UNSET,
) -> EventRecoveryNotificationRetryHistory | Problem | None:
    """List saved recovery notification retry decisions.

     Requires apps:read or admin and MFA. Lists at most 100 immutable request summaries in decision time
    order for the retained owned recovery job. The optional status filter selects a comma-separated
    union of original-generation request statuses. Totals cover all retained requests before filtering;
    matched_count counts returned rows. Other query parameters, repeated status parameters, duplicate
    statuses, empty selections, and unknown statuses are rejected. Decisions expire when the job is
    pruned.

    Args:
        job_id (UUID):
        status (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryNotificationRetryHistory | Problem
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        status=status,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    status: str | Unset = UNSET,
) -> Response[EventRecoveryNotificationRetryHistory | Problem]:
    """List saved recovery notification retry decisions.

     Requires apps:read or admin and MFA. Lists at most 100 immutable request summaries in decision time
    order for the retained owned recovery job. The optional status filter selects a comma-separated
    union of original-generation request statuses. Totals cover all retained requests before filtering;
    matched_count counts returned rows. Other query parameters, repeated status parameters, duplicate
    statuses, empty selections, and unknown statuses are rejected. Decisions expire when the job is
    pruned.

    Args:
        job_id (UUID):
        status (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryNotificationRetryHistory | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        status=status,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    status: str | Unset = UNSET,
) -> EventRecoveryNotificationRetryHistory | Problem | None:
    """List saved recovery notification retry decisions.

     Requires apps:read or admin and MFA. Lists at most 100 immutable request summaries in decision time
    order for the retained owned recovery job. The optional status filter selects a comma-separated
    union of original-generation request statuses. Totals cover all retained requests before filtering;
    matched_count counts returned rows. Other query parameters, repeated status parameters, duplicate
    statuses, empty selections, and unknown statuses are rejected. Decisions expire when the job is
    pruned.

    Args:
        job_id (UUID):
        status (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryNotificationRetryHistory | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            status=status,
        )
    ).parsed
