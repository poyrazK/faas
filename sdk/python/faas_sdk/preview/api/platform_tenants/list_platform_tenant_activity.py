from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_activity_response import PlatformTenantActivityResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    since: str | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    status: int | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    json_app_id: str | Unset = UNSET
    if not isinstance(app_id, Unset):
        json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["status"] = status

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/platform-tenants/{id}/activity".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantActivityResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantActivityResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantActivityResponse | Problem]:
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
    since: str | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    status: int | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> Response[PlatformTenantActivityResponse | Problem]:
    """Read retained request-debugger evidence across a platform tenant's apps.

     This plan-gated support view returns only bounded debugger evidence carrying request-time tenant
    attribution. It is sampled/retained evidence, not a complete request ledger or billing source of
    truth. Bodies, headers, and credentials are never returned.

    Args:
        id (UUID):
        since (str | Unset):
        app_id (UUID | Unset):
        status (int | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantActivityResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        since=since,
        app_id=app_id,
        status=status,
        limit=limit,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    status: int | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> PlatformTenantActivityResponse | Problem | None:
    """Read retained request-debugger evidence across a platform tenant's apps.

     This plan-gated support view returns only bounded debugger evidence carrying request-time tenant
    attribution. It is sampled/retained evidence, not a complete request ledger or billing source of
    truth. Bodies, headers, and credentials are never returned.

    Args:
        id (UUID):
        since (str | Unset):
        app_id (UUID | Unset):
        status (int | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantActivityResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        since=since,
        app_id=app_id,
        status=status,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    status: int | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> Response[PlatformTenantActivityResponse | Problem]:
    """Read retained request-debugger evidence across a platform tenant's apps.

     This plan-gated support view returns only bounded debugger evidence carrying request-time tenant
    attribution. It is sampled/retained evidence, not a complete request ledger or billing source of
    truth. Bodies, headers, and credentials are never returned.

    Args:
        id (UUID):
        since (str | Unset):
        app_id (UUID | Unset):
        status (int | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantActivityResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        since=since,
        app_id=app_id,
        status=status,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    status: int | Unset = UNSET,
    limit: int | Unset = 100,
    cursor: str | Unset = UNSET,
) -> PlatformTenantActivityResponse | Problem | None:
    """Read retained request-debugger evidence across a platform tenant's apps.

     This plan-gated support view returns only bounded debugger evidence carrying request-time tenant
    attribution. It is sampled/retained evidence, not a complete request ledger or billing source of
    truth. Bodies, headers, and credentials are never returned.

    Args:
        id (UUID):
        since (str | Unset):
        app_id (UUID | Unset):
        status (int | Unset):
        limit (int | Unset):  Default: 100.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantActivityResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            since=since,
            app_id=app_id,
            status=status,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
