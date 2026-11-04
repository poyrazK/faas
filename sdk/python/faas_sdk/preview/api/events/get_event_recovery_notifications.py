from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_notifications import EventRecoveryNotifications
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    job_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/event-recoveries/{job_id}/notifications".format(
            job_id=quote(str(job_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryNotifications | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryNotifications.from_dict(response.json())

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
) -> Response[EventRecoveryNotifications | Problem]:
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
) -> Response[EventRecoveryNotifications | Problem]:
    """Inspect recovery notification capture and receiver acknowledgements.

     Requires apps:read or admin and MFA. Read-only account-scoped report for a retained recovery job.
    Admission and execution notifications are separate. Preserved receiver snapshots and retained outbox
    rows can establish complete selection; retained deliveries alone cannot. Reports up to 100 receivers
    per notification and marks incomplete counts. Missing/pruned evidence is unknown, never proof of
    acknowledgement. Existing retry paths require their normal write scope and MFA. No query parameters
    are accepted.

    Args:
        job_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryNotifications | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventRecoveryNotifications | Problem | None:
    """Inspect recovery notification capture and receiver acknowledgements.

     Requires apps:read or admin and MFA. Read-only account-scoped report for a retained recovery job.
    Admission and execution notifications are separate. Preserved receiver snapshots and retained outbox
    rows can establish complete selection; retained deliveries alone cannot. Reports up to 100 receivers
    per notification and marks incomplete counts. Missing/pruned evidence is unknown, never proof of
    acknowledgement. Existing retry paths require their normal write scope and MFA. No query parameters
    are accepted.

    Args:
        job_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryNotifications | Problem
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventRecoveryNotifications | Problem]:
    """Inspect recovery notification capture and receiver acknowledgements.

     Requires apps:read or admin and MFA. Read-only account-scoped report for a retained recovery job.
    Admission and execution notifications are separate. Preserved receiver snapshots and retained outbox
    rows can establish complete selection; retained deliveries alone cannot. Reports up to 100 receivers
    per notification and marks incomplete counts. Missing/pruned evidence is unknown, never proof of
    acknowledgement. Existing retry paths require their normal write scope and MFA. No query parameters
    are accepted.

    Args:
        job_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryNotifications | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventRecoveryNotifications | Problem | None:
    """Inspect recovery notification capture and receiver acknowledgements.

     Requires apps:read or admin and MFA. Read-only account-scoped report for a retained recovery job.
    Admission and execution notifications are separate. Preserved receiver snapshots and retained outbox
    rows can establish complete selection; retained deliveries alone cannot. Reports up to 100 receivers
    per notification and marks incomplete counts. Missing/pruned evidence is unknown, never proof of
    acknowledgement. Existing retry paths require their normal write scope and MFA. No query parameters
    are accepted.

    Args:
        job_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryNotifications | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
        )
    ).parsed
