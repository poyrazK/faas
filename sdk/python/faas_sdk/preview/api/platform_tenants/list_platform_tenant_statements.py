import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_statement_list_response import PlatformTenantStatementListResponse
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    id: UUID,
    *,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_period_start = period_start.isoformat()
    params["period_start"] = json_period_start

    json_period_end = period_end.isoformat()
    params["period_end"] = json_period_end

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/platform-tenants/{id}/usage-statements".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantStatementListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantStatementListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantStatementListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
) -> Response[PlatformTenantStatementListResponse | Problem]:
    """List revisions of a customer's cross-app usage statement for one period.

    Args:
        id (UUID):
        period_start (datetime.datetime):
        period_end (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementListResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        period_start=period_start,
        period_end=period_end,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
) -> PlatformTenantStatementListResponse | Problem | None:
    """List revisions of a customer's cross-app usage statement for one period.

    Args:
        id (UUID):
        period_start (datetime.datetime):
        period_end (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementListResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        period_start=period_start,
        period_end=period_end,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
) -> Response[PlatformTenantStatementListResponse | Problem]:
    """List revisions of a customer's cross-app usage statement for one period.

    Args:
        id (UUID):
        period_start (datetime.datetime):
        period_end (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementListResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        period_start=period_start,
        period_end=period_end,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    period_start: datetime.datetime,
    period_end: datetime.datetime,
) -> PlatformTenantStatementListResponse | Problem | None:
    """List revisions of a customer's cross-app usage statement for one period.

    Args:
        id (UUID):
        period_start (datetime.datetime):
        period_end (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementListResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            period_start=period_start,
            period_end=period_end,
        )
    ).parsed
