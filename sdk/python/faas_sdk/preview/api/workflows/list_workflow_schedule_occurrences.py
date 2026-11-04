from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_workflow_schedule_occurrences_response import ListWorkflowScheduleOccurrencesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    platform_tenant_id: UUID | Unset = UNSET,
    cursor: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_platform_tenant_id: str | Unset = UNSET
    if not isinstance(platform_tenant_id, Unset):
        json_platform_tenant_id = str(platform_tenant_id)
    params["platform_tenant_id"] = json_platform_tenant_id

    json_cursor: str | Unset = UNSET
    if not isinstance(cursor, Unset):
        json_cursor = str(cursor)
    params["cursor"] = json_cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflows/schedules/occurrences".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListWorkflowScheduleOccurrencesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListWorkflowScheduleOccurrencesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListWorkflowScheduleOccurrencesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    platform_tenant_id: UUID | Unset = UNSET,
    cursor: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[ListWorkflowScheduleOccurrencesResponse | Problem]:
    """Inspect scheduled workflow admission history

     Started and skipped due minutes retained for 30 days. Requires app read access. History includes all
    linked tenants; optionally filter by tenant. No missed-minute catch-up is inferred.

    Args:
        slug (str):
        platform_tenant_id (UUID | Unset):
        cursor (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListWorkflowScheduleOccurrencesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        platform_tenant_id=platform_tenant_id,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    platform_tenant_id: UUID | Unset = UNSET,
    cursor: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> ListWorkflowScheduleOccurrencesResponse | Problem | None:
    """Inspect scheduled workflow admission history

     Started and skipped due minutes retained for 30 days. Requires app read access. History includes all
    linked tenants; optionally filter by tenant. No missed-minute catch-up is inferred.

    Args:
        slug (str):
        platform_tenant_id (UUID | Unset):
        cursor (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListWorkflowScheduleOccurrencesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        platform_tenant_id=platform_tenant_id,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    platform_tenant_id: UUID | Unset = UNSET,
    cursor: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[ListWorkflowScheduleOccurrencesResponse | Problem]:
    """Inspect scheduled workflow admission history

     Started and skipped due minutes retained for 30 days. Requires app read access. History includes all
    linked tenants; optionally filter by tenant. No missed-minute catch-up is inferred.

    Args:
        slug (str):
        platform_tenant_id (UUID | Unset):
        cursor (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListWorkflowScheduleOccurrencesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        platform_tenant_id=platform_tenant_id,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    platform_tenant_id: UUID | Unset = UNSET,
    cursor: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> ListWorkflowScheduleOccurrencesResponse | Problem | None:
    """Inspect scheduled workflow admission history

     Started and skipped due minutes retained for 30 days. Requires app read access. History includes all
    linked tenants; optionally filter by tenant. No missed-minute catch-up is inferred.

    Args:
        slug (str):
        platform_tenant_id (UUID | Unset):
        cursor (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListWorkflowScheduleOccurrencesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            platform_tenant_id=platform_tenant_id,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
