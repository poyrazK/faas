from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_preflight import EventRecoveryPreflight
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    job_id: UUID,
    *,
    retry: bool | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["retry"] = retry

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/event-recoveries/{job_id}/preflight".format(
            job_id=quote(str(job_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryPreflight | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryPreflight.from_dict(response.json())

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
) -> Response[EventRecoveryPreflight | Problem]:
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
    retry: bool | Unset = UNSET,
) -> Response[EventRecoveryPreflight | Problem]:
    """Assess a frozen recovery selection without admitting work.

     Requires apps:read or admin and MFA. Read-only observations of all pending items, with up to 100
    sample positions. Current per-item eligibility and capacity are not reservations. Conditions can
    change before admission. The optimistic rate-window estimate includes all pending admissions/skips
    and spent permits, assumes immediate resume, and excludes future contention, worker delays and
    handler execution. With retry=true, review every saved item of a terminal native workflow recovery
    using one consistent snapshot. Only saved failures matching the retained run generation can be
    retried. Capacity is temporary; changed, missing, unsafe, and resume-limit targets cannot be
    selected. This view never creates a job or resumes a run. Timing fields describe the parent job, not
    a prospective child.

    Args:
        job_id (UUID):
        retry (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryPreflight | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        retry=retry,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    retry: bool | Unset = UNSET,
) -> EventRecoveryPreflight | Problem | None:
    """Assess a frozen recovery selection without admitting work.

     Requires apps:read or admin and MFA. Read-only observations of all pending items, with up to 100
    sample positions. Current per-item eligibility and capacity are not reservations. Conditions can
    change before admission. The optimistic rate-window estimate includes all pending admissions/skips
    and spent permits, assumes immediate resume, and excludes future contention, worker delays and
    handler execution. With retry=true, review every saved item of a terminal native workflow recovery
    using one consistent snapshot. Only saved failures matching the retained run generation can be
    retried. Capacity is temporary; changed, missing, unsafe, and resume-limit targets cannot be
    selected. This view never creates a job or resumes a run. Timing fields describe the parent job, not
    a prospective child.

    Args:
        job_id (UUID):
        retry (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryPreflight | Problem
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        retry=retry,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    retry: bool | Unset = UNSET,
) -> Response[EventRecoveryPreflight | Problem]:
    """Assess a frozen recovery selection without admitting work.

     Requires apps:read or admin and MFA. Read-only observations of all pending items, with up to 100
    sample positions. Current per-item eligibility and capacity are not reservations. Conditions can
    change before admission. The optimistic rate-window estimate includes all pending admissions/skips
    and spent permits, assumes immediate resume, and excludes future contention, worker delays and
    handler execution. With retry=true, review every saved item of a terminal native workflow recovery
    using one consistent snapshot. Only saved failures matching the retained run generation can be
    retried. Capacity is temporary; changed, missing, unsafe, and resume-limit targets cannot be
    selected. This view never creates a job or resumes a run. Timing fields describe the parent job, not
    a prospective child.

    Args:
        job_id (UUID):
        retry (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryPreflight | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        retry=retry,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    retry: bool | Unset = UNSET,
) -> EventRecoveryPreflight | Problem | None:
    """Assess a frozen recovery selection without admitting work.

     Requires apps:read or admin and MFA. Read-only observations of all pending items, with up to 100
    sample positions. Current per-item eligibility and capacity are not reservations. Conditions can
    change before admission. The optimistic rate-window estimate includes all pending admissions/skips
    and spent permits, assumes immediate resume, and excludes future contention, worker delays and
    handler execution. With retry=true, review every saved item of a terminal native workflow recovery
    using one consistent snapshot. Only saved failures matching the retained run generation can be
    retried. Capacity is temporary; changed, missing, unsafe, and resume-limit targets cannot be
    selected. This view never creates a job or resumes a run. Timing fields describe the parent job, not
    a prospective child.

    Args:
        job_id (UUID):
        retry (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryPreflight | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            retry=retry,
        )
    ).parsed
