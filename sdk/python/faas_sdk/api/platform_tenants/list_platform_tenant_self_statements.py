import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_self_statement_list_response import PlatformTenantSelfStatementListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
    limit: int | Unset = 100,
    offset: int | Unset = 0,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_period_start = period_start.isoformat()
    params["period_start"] = json_period_start

    json_period_end = period_end.isoformat()
    params["period_end"] = json_period_end

    params["limit"] = limit

    params["offset"] = offset

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/usage-statements",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantSelfStatementListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantSelfStatementListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantSelfStatementListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
    limit: int | Unset = 100,
    offset: int | Unset = 0,
) -> Response[PlatformTenantSelfStatementListResponse | Problem]:
    """List this tenant's finalized cross-app usage statements.

     Requires a tenant-bound access token with platform_tenant:statements:read. Drafts and superseded
    revisions are never exposed. Statements overlapping the requested window are returned newest period
    and revision first.

    Args:
        period_start (datetime.datetime):
        period_end (datetime.datetime):
        limit (int | Unset):  Default: 100.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfStatementListResponse | Problem]
    """

    kwargs = _get_kwargs(
        period_start=period_start,
        period_end=period_end,
        limit=limit,
        offset=offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
    limit: int | Unset = 100,
    offset: int | Unset = 0,
) -> PlatformTenantSelfStatementListResponse | Problem | None:
    """List this tenant's finalized cross-app usage statements.

     Requires a tenant-bound access token with platform_tenant:statements:read. Drafts and superseded
    revisions are never exposed. Statements overlapping the requested window are returned newest period
    and revision first.

    Args:
        period_start (datetime.datetime):
        period_end (datetime.datetime):
        limit (int | Unset):  Default: 100.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfStatementListResponse | Problem
    """

    return sync_detailed(
        client=client,
        period_start=period_start,
        period_end=period_end,
        limit=limit,
        offset=offset,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
    limit: int | Unset = 100,
    offset: int | Unset = 0,
) -> Response[PlatformTenantSelfStatementListResponse | Problem]:
    """List this tenant's finalized cross-app usage statements.

     Requires a tenant-bound access token with platform_tenant:statements:read. Drafts and superseded
    revisions are never exposed. Statements overlapping the requested window are returned newest period
    and revision first.

    Args:
        period_start (datetime.datetime):
        period_end (datetime.datetime):
        limit (int | Unset):  Default: 100.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfStatementListResponse | Problem]
    """

    kwargs = _get_kwargs(
        period_start=period_start,
        period_end=period_end,
        limit=limit,
        offset=offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
    limit: int | Unset = 100,
    offset: int | Unset = 0,
) -> PlatformTenantSelfStatementListResponse | Problem | None:
    """List this tenant's finalized cross-app usage statements.

     Requires a tenant-bound access token with platform_tenant:statements:read. Drafts and superseded
    revisions are never exposed. Statements overlapping the requested window are returned newest period
    and revision first.

    Args:
        period_start (datetime.datetime):
        period_end (datetime.datetime):
        limit (int | Unset):  Default: 100.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfStatementListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            period_start=period_start,
            period_end=period_end,
            limit=limit,
            offset=offset,
        )
    ).parsed
