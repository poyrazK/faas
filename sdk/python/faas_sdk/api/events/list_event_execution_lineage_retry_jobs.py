from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_execution_lineage_retry_jobs import EventExecutionLineageRetryJobs
from ...models.list_event_execution_lineage_retry_jobs_status import (
    ListEventExecutionLineageRetryJobsStatus,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    status: ListEventExecutionLineageRetryJobsStatus | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/execution-reconciliation/retry-jobs".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventExecutionLineageRetryJobs | Problem | None:
    if response.status_code == 200:
        response_200 = EventExecutionLineageRetryJobs.from_dict(response.json())

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
) -> Response[EventExecutionLineageRetryJobs | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    status: ListEventExecutionLineageRetryJobsStatus | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventExecutionLineageRetryJobs | Problem]:
    """List retained bulk lineage retry job summaries.

     Requires read scope. Returns live retained jobs newest first by original creation time, with request
    UUID as a descending tie-breaker. Expired jobs are excluded before physical cleanup. Status and
    progress are current observations; pagination is not a frozen snapshot. Continue with next_after and
    the same status filter. Jobs created or changing into a filtered status above the cursor can require
    a fresh listing. Summaries omit case selections and results; GET an individual job for details or
    its original creation guard before cancellation.

    Args:
        slug (str):
        subscription_id (UUID):
        status (ListEventExecutionLineageRetryJobsStatus | Unset):
        after (str | Unset):
        limit (int | Unset): Zero selects the default page size. Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageRetryJobs | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        status=status,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    status: ListEventExecutionLineageRetryJobsStatus | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventExecutionLineageRetryJobs | Problem | None:
    """List retained bulk lineage retry job summaries.

     Requires read scope. Returns live retained jobs newest first by original creation time, with request
    UUID as a descending tie-breaker. Expired jobs are excluded before physical cleanup. Status and
    progress are current observations; pagination is not a frozen snapshot. Continue with next_after and
    the same status filter. Jobs created or changing into a filtered status above the cursor can require
    a fresh listing. Summaries omit case selections and results; GET an individual job for details or
    its original creation guard before cancellation.

    Args:
        slug (str):
        subscription_id (UUID):
        status (ListEventExecutionLineageRetryJobsStatus | Unset):
        after (str | Unset):
        limit (int | Unset): Zero selects the default page size. Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageRetryJobs | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        status=status,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    status: ListEventExecutionLineageRetryJobsStatus | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventExecutionLineageRetryJobs | Problem]:
    """List retained bulk lineage retry job summaries.

     Requires read scope. Returns live retained jobs newest first by original creation time, with request
    UUID as a descending tie-breaker. Expired jobs are excluded before physical cleanup. Status and
    progress are current observations; pagination is not a frozen snapshot. Continue with next_after and
    the same status filter. Jobs created or changing into a filtered status above the cursor can require
    a fresh listing. Summaries omit case selections and results; GET an individual job for details or
    its original creation guard before cancellation.

    Args:
        slug (str):
        subscription_id (UUID):
        status (ListEventExecutionLineageRetryJobsStatus | Unset):
        after (str | Unset):
        limit (int | Unset): Zero selects the default page size. Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventExecutionLineageRetryJobs | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        status=status,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    status: ListEventExecutionLineageRetryJobsStatus | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventExecutionLineageRetryJobs | Problem | None:
    """List retained bulk lineage retry job summaries.

     Requires read scope. Returns live retained jobs newest first by original creation time, with request
    UUID as a descending tie-breaker. Expired jobs are excluded before physical cleanup. Status and
    progress are current observations; pagination is not a frozen snapshot. Continue with next_after and
    the same status filter. Jobs created or changing into a filtered status above the cursor can require
    a fresh listing. Summaries omit case selections and results; GET an individual job for details or
    its original creation guard before cancellation.

    Args:
        slug (str):
        subscription_id (UUID):
        status (ListEventExecutionLineageRetryJobsStatus | Unset):
        after (str | Unset):
        limit (int | Unset): Zero selects the default page size. Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventExecutionLineageRetryJobs | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            status=status,
            after=after,
            limit=limit,
        )
    ).parsed
