import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_schedule_preview_response import WorkflowSchedulePreviewResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    at: datetime.datetime | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    count: int | Unset = 5,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_at: str | Unset = UNSET
    if not isinstance(at, Unset):
        json_at = at.isoformat()
    params["at"] = json_at

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params["count"] = count

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/apps/{slug}/workflows/schedules/{name}/preview".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowSchedulePreviewResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowSchedulePreviewResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowSchedulePreviewResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    at: datetime.datetime | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    count: int | Unset = 5,
) -> Response[Problem | WorkflowSchedulePreviewResponse]:
    """Preview fire times and catch-up for this tenant's schedule.

     Read-only simulation using this tenant's effective schedule and durable cursor. Fire times follow
    Gregale daylight-saving rules. No run is admitted or cursor changed.

    Args:
        slug (str):
        name (str):
        at (datetime.datetime | Unset):
        since (datetime.datetime | Unset):
        count (int | Unset):  Default: 5.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowSchedulePreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        at=at,
        since=since,
        count=count,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    at: datetime.datetime | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    count: int | Unset = 5,
) -> Problem | WorkflowSchedulePreviewResponse | None:
    """Preview fire times and catch-up for this tenant's schedule.

     Read-only simulation using this tenant's effective schedule and durable cursor. Fire times follow
    Gregale daylight-saving rules. No run is admitted or cursor changed.

    Args:
        slug (str):
        name (str):
        at (datetime.datetime | Unset):
        since (datetime.datetime | Unset):
        count (int | Unset):  Default: 5.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowSchedulePreviewResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        at=at,
        since=since,
        count=count,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    at: datetime.datetime | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    count: int | Unset = 5,
) -> Response[Problem | WorkflowSchedulePreviewResponse]:
    """Preview fire times and catch-up for this tenant's schedule.

     Read-only simulation using this tenant's effective schedule and durable cursor. Fire times follow
    Gregale daylight-saving rules. No run is admitted or cursor changed.

    Args:
        slug (str):
        name (str):
        at (datetime.datetime | Unset):
        since (datetime.datetime | Unset):
        count (int | Unset):  Default: 5.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowSchedulePreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        at=at,
        since=since,
        count=count,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient,
    at: datetime.datetime | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    count: int | Unset = 5,
) -> Problem | WorkflowSchedulePreviewResponse | None:
    """Preview fire times and catch-up for this tenant's schedule.

     Read-only simulation using this tenant's effective schedule and durable cursor. Fire times follow
    Gregale daylight-saving rules. No run is admitted or cursor changed.

    Args:
        slug (str):
        name (str):
        at (datetime.datetime | Unset):
        since (datetime.datetime | Unset):
        count (int | Unset):  Default: 5.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowSchedulePreviewResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            at=at,
            since=since,
            count=count,
        )
    ).parsed
