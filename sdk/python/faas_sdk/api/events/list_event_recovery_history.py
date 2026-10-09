from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_history import EventRecoveryHistory
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    job_id: UUID,
    *,
    after: int | Unset = 0,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/event-recoveries/{job_id}/history".format(
            job_id=quote(str(job_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryHistory | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryHistory.from_dict(response.json())

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

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRecoveryHistory | Problem]:
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
    after: int | Unset = 0,
    limit: int | Unset = 100,
) -> Response[EventRecoveryHistory | Problem]:
    """Inspect retained recovery control audit history.

     Requires apps:read or admin and MFA. Entries are ordered by ID ascending. Actor identity is server-
    resolved. Failed requests and no-op controls add no entry. Expiry discovered by controls commits a
    system entry even when the request returns 409. History is retained and deleted with its job.
    Existing jobs have no fabricated historical entries.

    Args:
        job_id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryHistory | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
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
    after: int | Unset = 0,
    limit: int | Unset = 100,
) -> EventRecoveryHistory | Problem | None:
    """Inspect retained recovery control audit history.

     Requires apps:read or admin and MFA. Entries are ordered by ID ascending. Actor identity is server-
    resolved. Failed requests and no-op controls add no entry. Expiry discovered by controls commits a
    system entry even when the request returns 409. History is retained and deleted with its job.
    Existing jobs have no fabricated historical entries.

    Args:
        job_id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryHistory | Problem
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
    limit: int | Unset = 100,
) -> Response[EventRecoveryHistory | Problem]:
    """Inspect retained recovery control audit history.

     Requires apps:read or admin and MFA. Entries are ordered by ID ascending. Actor identity is server-
    resolved. Failed requests and no-op controls add no entry. Expiry discovered by controls commits a
    system entry even when the request returns 409. History is retained and deleted with its job.
    Existing jobs have no fabricated historical entries.

    Args:
        job_id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryHistory | Problem]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
    limit: int | Unset = 100,
) -> EventRecoveryHistory | Problem | None:
    """Inspect retained recovery control audit history.

     Requires apps:read or admin and MFA. Entries are ordered by ID ascending. Actor identity is server-
    resolved. Failed requests and no-op controls add no entry. Expiry discovered by controls commits a
    system entry even when the request returns 409. History is retained and deleted with its job.
    Existing jobs have no fabricated historical entries.

    Args:
        job_id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryHistory | Problem
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            after=after,
            limit=limit,
        )
    ).parsed
